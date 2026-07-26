const std = @import("std");
const Io = std.Io;
const path_mod = std.fs.path;
const limits = @import("limits.zig");
const block_cache = @import("block_cache.zig");
const proxy = @import("proxy.zig");

pub fn main(init: std.process.Init) !void {
    const arena = init.arena.allocator();
    const io = init.io;
    const gpa = init.gpa;

    const args = try init.minimal.args.toSlice(arena);
    const opts = try parseArgs(args);

    const file = try openOrigin(io, opts.file_path);
    errdefer file.close(io);

    const st = try file.stat(io);
    if (st.size == 0) {
        std.log.err("origin file is empty: {s}", .{opts.file_path});
        return error.EmptyOrigin;
    }

    const storage_len = @as(usize, limits.CACHE_BLOCKS) * limits.BLOCK_SIZE;
    const storage = try gpa.alignedAlloc(u8, .fromByteUnits(std.heap.pageSize()), storage_len);
    defer gpa.free(storage);

    var cache = block_cache.BlockCache.init(io, file, st.size, storage);
    var state = proxy.State{
        .io = io,
        .cache = &cache,
        .object_name = opts.file_path,
    };

    try proxy.serveAddress(&state, opts.port);
}

const Options = struct {
    file_path: []const u8,
    port: u16,
};

fn parseArgs(args: []const []const u8) !Options {
    // args[0] is executable path
    var file_path: ?[]const u8 = null;
    var port: u16 = 8080;

    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        const a = args[i];
        if (std.mem.eql(u8, a, "--file")) {
            i += 1;
            if (i >= args.len) return error.MissingFile;
            file_path = args[i];
        } else if (std.mem.eql(u8, a, "--port")) {
            i += 1;
            if (i >= args.len) return error.MissingPort;
            port = try std.fmt.parseInt(u16, args[i], 10);
        } else if (std.mem.eql(u8, a, "--help") or std.mem.eql(u8, a, "-h")) {
            std.log.info("usage: stream_proxy --file PATH [--port 8080]", .{});
            return error.Help;
        } else {
            std.log.err("unknown arg: {s}", .{a});
            return error.UnknownArg;
        }
    }

    return .{
        .file_path = file_path orelse {
            std.log.err("missing --file PATH", .{});
            return error.MissingFile;
        },
        .port = port,
    };
}

fn openOrigin(io: Io, file_path: []const u8) !Io.File {
    if (path_mod.isAbsolute(file_path)) {
        return Io.Dir.openFileAbsolute(io, file_path, .{ .mode = .read_only });
    }
    return Io.Dir.cwd().openFile(io, file_path, .{ .mode = .read_only });
}

test {
    _ = @import("limits.zig");
    _ = @import("block_cache.zig");
    _ = @import("proxy.zig");
}
