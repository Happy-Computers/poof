const std = @import("std");
const Io = std.Io;
const path_mod = std.fs.path;
const http = std.http;
const limits = @import("limits.zig");
const block_cache = @import("block_cache.zig");
const origin_mod = @import("origin.zig");
const proxy = @import("proxy.zig");
const uds = @import("uds.zig");

pub fn main(init: std.process.Init) !void {
    const arena = init.arena.allocator();
    const io = init.io;
    const gpa = init.gpa;

    const args = try init.minimal.args.toSlice(arena);
    const opts = try parseArgs(args);

    var http_client: ?http.Client = null;
    defer if (http_client) |*c| c.deinit();

    var file_origin_storage: ?origin_mod.FileOrigin = null;
    var http_origin_storage: ?origin_mod.HttpOrigin = null;
    var origin: origin_mod.Origin = undefined;
    var object_size: u64 = undefined;
    var object_name: []const u8 = undefined;
    var object_basename: []const u8 = undefined;

    if (opts.file_path) |file_path| {
        const file = try openFile(io, file_path);
        // File stays open for process lifetime (origin owns it).
        const st = try file.stat(io);
        if (st.size == 0) {
            std.log.err("origin file is empty: {s}", .{file_path});
            return error.EmptyOrigin;
        }
        file_origin_storage = .{ .io = io, .file = file };
        origin = (&file_origin_storage.?).origin();
        object_size = st.size;
        object_name = file_path;
        object_basename = opts.name orelse path_mod.basename(file_path);
    } else if (opts.origin_url) |origin_url| {
        http_client = .{
            .allocator = gpa,
            .io = io,
        };
        http_origin_storage = .{
            .io = io,
            .client = &http_client.?,
            .url = origin_url,
        };
        object_size = try (&http_origin_storage.?).headSize();
        origin = (&http_origin_storage.?).origin();
        object_name = origin_url;
        object_basename = opts.name orelse basenameFromUrl(origin_url);
        std.log.info("http origin size={d} url={s}", .{ object_size, origin_url });
    } else unreachable;

    const storage_len = @as(usize, limits.CACHE_BLOCKS) * limits.BLOCK_SIZE;
    const storage = try gpa.alignedAlloc(u8, .fromByteUnits(std.heap.pageSize()), storage_len);
    defer gpa.free(storage);

    var cache = block_cache.BlockCache.init(io, origin, object_size, storage);
    var state = proxy.State{
        .io = io,
        .cache = &cache,
        .object_name = object_name,
        .object_basename = object_basename,
    };

    var group: Io.Group = .init;
    defer group.cancel(io);

    if (opts.uds_path) |uds_path| {
        try group.concurrent(io, serveUds, .{ &state, uds_path });
    }

    if (opts.port) |port| {
        try proxy.serveAddress(&state, port);
    } else if (opts.uds_path != null) {
        try group.await(io);
    } else {
        std.log.err("need --port and/or --uds", .{});
        return error.MissingListen;
    }
}

fn serveUds(state: *proxy.State, uds_path: []const u8) void {
    uds.servePath(state, uds_path) catch |err| {
        std.log.err("uds serve failed: {t}", .{err});
    };
}

const Options = struct {
    file_path: ?[]const u8,
    origin_url: ?[]const u8,
    name: ?[]const u8,
    port: ?u16,
    uds_path: ?[]const u8,
};

fn parseArgs(args: []const []const u8) !Options {
    var file_path: ?[]const u8 = null;
    var origin_url: ?[]const u8 = null;
    var name: ?[]const u8 = null;
    var port: ?u16 = 8080;
    var uds_path: ?[]const u8 = null;
    var port_set = false;
    var no_http = false;

    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        const a = args[i];
        if (std.mem.eql(u8, a, "--file")) {
            i += 1;
            if (i >= args.len) return error.MissingFile;
            file_path = args[i];
        } else if (std.mem.eql(u8, a, "--origin-url")) {
            i += 1;
            if (i >= args.len) return error.MissingOriginUrl;
            origin_url = args[i];
        } else if (std.mem.eql(u8, a, "--name")) {
            i += 1;
            if (i >= args.len) return error.MissingName;
            name = args[i];
        } else if (std.mem.eql(u8, a, "--port")) {
            i += 1;
            if (i >= args.len) return error.MissingPort;
            port = try std.fmt.parseInt(u16, args[i], 10);
            port_set = true;
        } else if (std.mem.eql(u8, a, "--uds")) {
            i += 1;
            if (i >= args.len) return error.MissingUds;
            uds_path = args[i];
        } else if (std.mem.eql(u8, a, "--no-http")) {
            no_http = true;
        } else if (std.mem.eql(u8, a, "--help") or std.mem.eql(u8, a, "-h")) {
            std.log.info(
                "usage: stream_proxy (--file PATH | --origin-url URL) [--name NAME] [--port 8080] [--uds PATH] [--no-http]",
                .{},
            );
            return error.Help;
        } else {
            std.log.err("unknown arg: {s}", .{a});
            return error.UnknownArg;
        }
    }

    if (file_path != null and origin_url != null) {
        std.log.err("--file and --origin-url are mutually exclusive", .{});
        return error.ConflictingArgs;
    }
    if (file_path == null and origin_url == null) {
        std.log.err("need --file PATH or --origin-url URL", .{});
        return error.MissingOrigin;
    }

    if (no_http) {
        if (port_set) {
            std.log.err("--no-http conflicts with --port", .{});
            return error.ConflictingArgs;
        }
        port = null;
    }

    return .{
        .file_path = file_path,
        .origin_url = origin_url,
        .name = name,
        .port = port,
        .uds_path = uds_path,
    };
}

fn openFile(io: Io, file_path: []const u8) !Io.File {
    if (path_mod.isAbsolute(file_path)) {
        return Io.Dir.openFileAbsolute(io, file_path, .{ .mode = .read_only });
    }
    return Io.Dir.cwd().openFile(io, file_path, .{ .mode = .read_only });
}

fn basenameFromUrl(url: []const u8) []const u8 {
    const path = if (std.mem.lastIndexOfScalar(u8, url, '/')) |i| url[i + 1 ..] else url;
    if (path.len == 0) return "object";
    return path;
}

test {
    _ = @import("limits.zig");
    _ = @import("origin.zig");
    _ = @import("block_cache.zig");
    _ = @import("protocol.zig");
    _ = @import("proxy.zig");
    _ = @import("uds.zig");
}
