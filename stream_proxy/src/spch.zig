const std = @import("std");
const Io = std.Io;
const net = Io.net;
const assert = std.debug.assert;
const limits = @import("limits.zig");
const protocol = @import("protocol.zig");
const proxy = @import("proxy.zig");

/// Serve the SPCH binary protocol on an accepted stream (UDS or TCP).
pub fn handleConnection(state: *proxy.State, stream: net.Stream) void {
    const io = state.io;
    defer {
        state.releaseConnection();
        var copy = stream;
        copy.close(io);
    }

    var recv_buf: [4096]u8 = undefined;
    var send_buf: [64 * 1024]u8 = undefined;
    const payload = std.heap.page_allocator.alloc(u8, limits.MAX_RANGE_BYTES) catch {
        std.log.err("spch: failed to allocate payload buffer", .{});
        return;
    };
    defer std.heap.page_allocator.free(payload);

    var reader = stream.reader(io, &recv_buf);
    var writer = stream.writer(io, &send_buf);

    while (true) {
        handleOne(state, &reader.interface, &writer.interface, payload) catch |err| switch (err) {
            error.EndOfStream => return,
            else => {
                std.log.err("spch request: {t}", .{err});
                return;
            },
        };
    }
}

fn handleOne(
    state: *proxy.State,
    reader: *Io.Reader,
    writer: *Io.Writer,
    payload: []u8,
) !void {
    const hdr_bytes = try reader.takeArray(protocol.Request.size);
    const req = protocol.Request.decode(hdr_bytes) catch {
        try writeStatus(writer, .invalid, &.{});
        return;
    };

    const op: protocol.Op = @enumFromInt(req.op);
    switch (op) {
        .size => {
            var size_bytes: [8]u8 = undefined;
            std.mem.writeInt(u64, &size_bytes, state.cache.object_size, .little);
            try writeStatus(writer, .ok, &size_bytes);
        },
        .info => {
            try writeStatus(writer, .ok, state.object_basename);
        },
        .prefetch => {
            state.cache.prefetchAhead();
            try writeStatus(writer, .ok, &.{});
        },
        .metrics => {
            const m = protocol.MetricsPayload{
                .bytes_from_origin = state.cache.metrics.bytes_from_origin.load(.monotonic),
                .bytes_to_client = state.cache.metrics.bytes_to_client.load(.monotonic),
                .cache_hits = state.cache.metrics.cache_hits.load(.monotonic),
                .cache_misses = state.cache.metrics.cache_misses.load(.monotonic),
                .cache_occupancy_blocks = state.cache.occupancyBlocks(),
                .prefetch_bytes = state.cache.metrics.prefetch_bytes.load(.monotonic),
                .prefetch_cancelled = state.cache.metrics.prefetch_cancelled.load(.monotonic),
                .object_size = state.cache.object_size,
            };
            const encoded = m.encode();
            try writeStatus(writer, .ok, &encoded);
        },
        .read => {
            try handleRead(state, writer, req, payload);
        },
        _ => try writeStatus(writer, .invalid, &.{}),
    }
}

fn handleRead(
    state: *proxy.State,
    writer: *Io.Writer,
    req: protocol.Request,
    payload: []u8,
) !void {
    if (req.length == 0) {
        try writeStatus(writer, .invalid, &.{});
        return;
    }
    if (req.length > limits.MAX_RANGE_BYTES) {
        try writeStatus(writer, .too_large, &.{});
        return;
    }
    if (req.offset >= state.cache.object_size) {
        try writeStatus(writer, .range, &.{});
        return;
    }

    const remaining = state.cache.object_size - req.offset;
    const want: u32 = @intCast(@min(remaining, req.length));
    assert(want > 0);
    const end_inclusive = req.offset + want - 1;

    const n = state.cache.copyRangeToSlice(req.offset, end_inclusive, payload[0..want]) catch {
        try writeStatus(writer, .io, &.{});
        return;
    };
    assert(n == want);
    try writeStatus(writer, .ok, payload[0..n]);
    state.cache.prefetchAhead();
}

fn writeStatus(writer: *Io.Writer, status: protocol.Status, payload: []const u8) !void {
    assert(payload.len <= std.math.maxInt(u32));
    const hdr = protocol.ResponseHeader{
        .status = @intFromEnum(status),
        .nbytes = @intCast(payload.len),
    };
    const hdr_bytes = hdr.encode();
    try writer.writeAll(&hdr_bytes);
    if (payload.len > 0) try writer.writeAll(payload);
    try writer.flush();
}
