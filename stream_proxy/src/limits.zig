//! Hard limits for the stream proxy. Change only by deliberate redesign.

pub const BLOCK_SIZE: u32 = 1024 * 1024;
pub const CACHE_BLOCKS: u32 = 512;
pub const PREFETCH_BLOCKS: u32 = 8;
pub const MAX_RANGE_BYTES: u32 = 8 * 1024 * 1024;
pub const LISTEN_BACKLOG: u31 = 16;

comptime {
    const std = @import("std");
    std.debug.assert(BLOCK_SIZE > 0);
    std.debug.assert(CACHE_BLOCKS > 0);
    std.debug.assert(PREFETCH_BLOCKS > 0);
    std.debug.assert(MAX_RANGE_BYTES >= BLOCK_SIZE);
    std.debug.assert(MAX_RANGE_BYTES % BLOCK_SIZE == 0);
}
