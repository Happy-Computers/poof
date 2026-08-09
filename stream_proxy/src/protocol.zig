//! Binary UDS protocol for the Go mount ↔ Zig cache seam.
//! Little-endian. Fixed 24-byte request header; response is status+nbytes+payload.

const std = @import("std");
const limits = @import("limits.zig");

pub const Op = enum(u16) {
    size = 1,
    read = 2,
    prefetch = 3,
    metrics = 4,
    info = 5,
    _,
};

pub const Status = enum(u32) {
    ok = 0,
    invalid = 1,
    range = 2,
    too_large = 3,
    io = 4,
    busy = 5,
};

pub const Request = extern struct {
    magic: u32,
    version: u16,
    op: u16,
    offset: u64,
    length: u32,
    reserved: u32 = 0,

    pub const size = @sizeOf(Request);
    comptime {
        std.debug.assert(size == 24);
    }

    pub fn decode(bytes: *const [size]u8) error{Invalid}!Request {
        const req: Request = @bitCast(bytes.*);
        if (req.magic != limits.UDS_MAGIC) return error.Invalid;
        if (req.version != limits.UDS_VERSION) return error.Invalid;
        return req;
    }

    pub fn encode(self: Request) [size]u8 {
        return @bitCast(self);
    }
};

pub const ResponseHeader = extern struct {
    status: u32,
    nbytes: u32,

    pub const size = @sizeOf(ResponseHeader);
    comptime {
        std.debug.assert(size == 8);
    }

    pub fn encode(self: ResponseHeader) [size]u8 {
        return @bitCast(self);
    }

    pub fn decode(bytes: *const [size]u8) ResponseHeader {
        return @bitCast(bytes.*);
    }
};

/// Fixed metrics payload for Op.metrics (all little-endian u64).
pub const MetricsPayload = extern struct {
    bytes_from_origin: u64,
    bytes_to_client: u64,
    cache_hits: u64,
    cache_misses: u64,
    cache_occupancy_blocks: u64,
    prefetch_bytes: u64,
    prefetch_cancelled: u64,
    object_size: u64,

    pub const size = @sizeOf(MetricsPayload);
    comptime {
        std.debug.assert(size == 64);
    }

    pub fn encode(self: MetricsPayload) [size]u8 {
        return @bitCast(self);
    }
};

test "request roundtrip" {
    const req = Request{
        .magic = limits.UDS_MAGIC,
        .version = limits.UDS_VERSION,
        .op = @intFromEnum(Op.read),
        .offset = 1024,
        .length = 4096,
    };
    const bytes = req.encode();
    const got = try Request.decode(&bytes);
    try std.testing.expectEqual(req.magic, got.magic);
    try std.testing.expectEqual(req.offset, got.offset);
    try std.testing.expectEqual(req.length, got.length);
}

test "reject bad magic" {
    var bytes = [_]u8{0} ** Request.size;
    try std.testing.expectError(error.Invalid, Request.decode(&bytes));
}
