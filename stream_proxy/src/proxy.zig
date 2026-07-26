const std = @import("std");
const http = std.http;
const Io = std.Io;
const net = Io.net;
const assert = std.debug.assert;
const limits = @import("limits.zig");
const block_cache = @import("block_cache.zig");

pub const State = struct {
    io: Io,
    cache: *block_cache.BlockCache,
    object_name: []const u8,
};

pub fn serveAddress(state: *State, port: u16) !void {
    var addr_buf: [32]u8 = undefined;
    const addr_text = try std.fmt.bufPrint(&addr_buf, "127.0.0.1:{d}", .{port});
    const address = try net.IpAddress.parseLiteral(addr_text);

    var server = try address.listen(state.io, .{
        .reuse_address = true,
    });
    defer server.deinit(state.io);

    std.log.info("stream_proxy listening on http://{f}/video.mp4", .{server.socket.address});
    std.log.info("object={s} size={d} cache={d}x{d}B", .{
        state.object_name,
        state.cache.object_size,
        limits.CACHE_BLOCKS,
        limits.BLOCK_SIZE,
    });

    var group: Io.Group = .init;
    defer group.cancel(state.io);

    while (true) {
        const stream = server.accept(state.io) catch |err| switch (err) {
            error.Canceled => return,
            else => {
                std.log.err("accept failed: {t}", .{err});
                continue;
            },
        };
        group.concurrent(state.io, handleConnection, .{ state, stream }) catch {
            var copy = stream;
            copy.close(state.io);
            std.log.err("failed to spawn connection handler", .{});
        };
    }
}

fn handleConnection(state: *State, stream: net.Stream) void {
    const io = state.io;
    defer {
        var copy = stream;
        copy.close(io);
    }

    var send_buffer: [8192]u8 = undefined;
    var recv_buffer: [8192]u8 = undefined;
    var connection_reader = stream.reader(io, &recv_buffer);
    var connection_writer = stream.writer(io, &send_buffer);
    var http_server: http.Server = .init(&connection_reader.interface, &connection_writer.interface);

    while (true) {
        var request = http_server.receiveHead() catch |err| switch (err) {
            error.HttpConnectionClosing => return,
            else => {
                std.log.err("receiveHead: {t}", .{err});
                return;
            },
        };
        handleRequest(state, &request) catch |err| {
            std.log.err("handleRequest {s}: {t}", .{ request.head.target, err });
            return;
        };
    }
}

fn handleRequest(state: *State, request: *http.Server.Request) !void {
    const path = pathOnly(request.head.target);
    const range_hdr = findHeader(request, "range");
    std.log.info("{s} {s} range={s}", .{
        @tagName(request.head.method),
        path,
        range_hdr orelse "-",
    });
    if (std.mem.eql(u8, path, "/metrics")) {
        try respondMetrics(state, request);
        return;
    }
    if (!std.mem.eql(u8, path, "/video.mp4") and !std.mem.eql(u8, path, "/")) {
        try request.respond("not found\n", .{
            .status = .not_found,
            .extra_headers = &.{
                .{ .name = "content-type", .value = "text/plain" },
            },
        });
        return;
    }

    switch (request.head.method) {
        .HEAD => try respondHead(state, request),
        .GET => try respondGet(state, request),
        else => try request.respond("method not allowed\n", .{
            .status = .method_not_allowed,
            .extra_headers = &.{
                .{ .name = "content-type", .value = "text/plain" },
                .{ .name = "allow", .value = "GET, HEAD" },
            },
        }),
    }
}

fn respondHead(state: *State, request: *http.Server.Request) !void {
    var size_buf: [32]u8 = undefined;
    const size_text = try std.fmt.bufPrint(&size_buf, "{d}", .{state.cache.object_size});
    try request.respond("", .{
        .status = .ok,
        .transfer_encoding = .none,
        .extra_headers = &.{
            .{ .name = "accept-ranges", .value = "bytes" },
            .{ .name = "content-type", .value = "video/mp4" },
            .{ .name = "content-length", .value = size_text },
        },
    });
}

fn respondGet(state: *State, request: *http.Server.Request) !void {
    const range_header = findHeader(request, "range") orelse {
        try request.respond("Range header required\n", .{
            .status = .bad_request,
            .extra_headers = &.{
                .{ .name = "content-type", .value = "text/plain" },
                .{ .name = "accept-ranges", .value = "bytes" },
            },
        });
        return;
    };

    const parsed = parseBytesRange(range_header, state.cache.object_size) catch {
        var cr_buf: [48]u8 = undefined;
        const cr = try std.fmt.bufPrint(&cr_buf, "bytes */{d}", .{state.cache.object_size});
        try request.respond("", .{
            .status = .range_not_satisfiable,
            .extra_headers = &.{
                .{ .name = "content-range", .value = cr },
            },
        });
        return;
    };

    const len = parsed.end_inclusive - parsed.start + 1;
    if (len > limits.MAX_RANGE_BYTES) {
        try request.respond("range too large\n", .{
            .status = .payload_too_large,
            .extra_headers = &.{
                .{ .name = "content-type", .value = "text/plain" },
            },
        });
        return;
    }

    var cr_buf: [96]u8 = undefined;
    const content_range = try std.fmt.bufPrint(&cr_buf, "bytes {d}-{d}/{d}", .{
        parsed.start,
        parsed.end_inclusive,
        state.cache.object_size,
    });

    var send_buf: [64 * 1024]u8 = undefined;
    var body = try request.respondStreaming(&send_buf, .{
        .content_length = len,
        .respond_options = .{
            .status = .partial_content,
            .extra_headers = &.{
                .{ .name = "accept-ranges", .value = "bytes" },
                .{ .name = "content-type", .value = "video/mp4" },
                .{ .name = "content-range", .value = content_range },
            },
        },
    });

    try state.cache.copyRange(parsed.start, parsed.end_inclusive, &body.writer);
    try body.end();
    state.cache.prefetchAhead();
}

fn respondMetrics(state: *State, request: *http.Server.Request) !void {
    var buf: [512]u8 = undefined;
    const body = try std.fmt.bufPrint(&buf,
        \\bytes_from_origin {d}
        \\bytes_to_client {d}
        \\cache_hits {d}
        \\cache_misses {d}
        \\cache_occupancy_blocks {d}
        \\prefetch_bytes {d}
        \\prefetch_cancelled {d}
        \\object_size {d}
        \\
    , .{
        state.cache.metrics.bytes_from_origin.load(.monotonic),
        state.cache.metrics.bytes_to_client.load(.monotonic),
        state.cache.metrics.cache_hits.load(.monotonic),
        state.cache.metrics.cache_misses.load(.monotonic),
        state.cache.occupancyBlocks(),
        state.cache.metrics.prefetch_bytes.load(.monotonic),
        state.cache.metrics.prefetch_cancelled.load(.monotonic),
        state.cache.object_size,
    });
    try request.respond(body, .{
        .status = .ok,
        .extra_headers = &.{
            .{ .name = "content-type", .value = "text/plain; charset=utf-8" },
        },
    });
}

const ByteRange = struct {
    start: u64,
    end_inclusive: u64,
};

fn parseBytesRange(header_value: []const u8, object_size: u64) error{InvalidRange}!ByteRange {
    assert(object_size > 0);
    if (!std.mem.startsWith(u8, header_value, "bytes=")) return error.InvalidRange;
    const spec = header_value["bytes=".len..];
    if (std.mem.indexOfScalar(u8, spec, ',') != null) return error.InvalidRange;

    const dash = std.mem.indexOfScalar(u8, spec, '-') orelse return error.InvalidRange;
    const start_text = spec[0..dash];
    const end_text = spec[dash + 1 ..];

    if (start_text.len == 0) {
        if (end_text.len == 0) return error.InvalidRange;
        const suffix = std.fmt.parseInt(u64, end_text, 10) catch return error.InvalidRange;
        if (suffix == 0) return error.InvalidRange;
        if (suffix >= object_size) {
            return .{ .start = 0, .end_inclusive = object_size - 1 };
        }
        return .{ .start = object_size - suffix, .end_inclusive = object_size - 1 };
    }

    const start = std.fmt.parseInt(u64, start_text, 10) catch return error.InvalidRange;
    if (start >= object_size) return error.InvalidRange;

    if (end_text.len == 0) {
        // VLC sends "bytes=0-". Returning less than (size-start) makes VLC's
        // HTTP access hit EOF and freeze the playhead — so serve through EOF,
        // capped only by MAX_RANGE_BYTES for multi-GB objects.
        const remaining = object_size - start;
        const window: u64 = @min(remaining, limits.MAX_RANGE_BYTES);
        assert(window > 0);
        return .{
            .start = start,
            .end_inclusive = start + window - 1,
        };
    }

    const end = std.fmt.parseInt(u64, end_text, 10) catch return error.InvalidRange;
    if (end < start) return error.InvalidRange;
    const end_inclusive = @min(end, object_size - 1);
    return .{ .start = start, .end_inclusive = end_inclusive };
}

fn findHeader(request: *const http.Server.Request, name: []const u8) ?[]const u8 {
    var it = request.iterateHeaders();
    while (it.next()) |h| {
        if (std.ascii.eqlIgnoreCase(h.name, name)) return h.value;
    }
    return null;
}

fn pathOnly(target: []const u8) []const u8 {
    if (std.mem.indexOfScalar(u8, target, '?')) |q| return target[0..q];
    return target;
}

test "parse bytes range" {
    const r1 = try parseBytesRange("bytes=0-99", 1000);
    try std.testing.expectEqual(@as(u64, 0), r1.start);
    try std.testing.expectEqual(@as(u64, 99), r1.end_inclusive);

    const r2 = try parseBytesRange("bytes=10-", 100);
    try std.testing.expectEqual(@as(u64, 10), r2.start);
    try std.testing.expectEqual(@as(u64, 99), r2.end_inclusive);

    const big = try parseBytesRange("bytes=0-", 100 * 1024 * 1024);
    try std.testing.expectEqual(@as(u64, 0), big.start);
    try std.testing.expectEqual(@as(u64, limits.MAX_RANGE_BYTES - 1), big.end_inclusive);

    const r3 = try parseBytesRange("bytes=-20", 100);
    try std.testing.expectEqual(@as(u64, 80), r3.start);
    try std.testing.expectEqual(@as(u64, 99), r3.end_inclusive);

    try std.testing.expectError(error.InvalidRange, parseBytesRange("bytes=0-10,11-20", 100));
    try std.testing.expectError(error.InvalidRange, parseBytesRange("bytes=100-200", 100));
}
