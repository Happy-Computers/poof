//! Hard limits for the stream proxy. Change only by deliberate redesign.

pub const BLOCK_SIZE: u32 = 1024 * 1024;
pub const CACHE_BLOCKS: u32 = 512;
pub const PREFETCH_BLOCKS: u32 = 8;
pub const MAX_RANGE_BYTES: u32 = 8 * 1024 * 1024;
pub const LISTEN_BACKLOG: u31 = 16;
/// Cap concurrent HTTP + UDS connection handlers.
pub const MAX_CONNECTIONS: u32 = 32;
/// Cap concurrent origin block fills. Today fills also serialize on the cache
/// mutex; this limit stays explicit so unlocking across origin I/O later cannot
/// silently unbounded-parallelize disk reads.
pub const MAX_CONCURRENT_ORIGIN_FILLS: u32 = 4;

/// UDS protocol magic: "SPCH" (Space Cache).
pub const UDS_MAGIC: u32 = 0x53504348;
pub const UDS_VERSION: u16 = 1;

comptime {
    const std = @import("std");
    std.debug.assert(BLOCK_SIZE > 0);
    std.debug.assert(CACHE_BLOCKS > 0);
    std.debug.assert(PREFETCH_BLOCKS > 0);
    std.debug.assert(MAX_RANGE_BYTES >= BLOCK_SIZE);
    std.debug.assert(MAX_RANGE_BYTES % BLOCK_SIZE == 0);
    std.debug.assert(LISTEN_BACKLOG > 0);
    std.debug.assert(MAX_CONNECTIONS > 0);
    std.debug.assert(MAX_CONCURRENT_ORIGIN_FILLS > 0);
}
