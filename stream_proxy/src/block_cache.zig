const std = @import("std");
const Io = std.Io;
const assert = std.debug.assert;
const limits = @import("limits.zig");
const origin_mod = @import("origin.zig");

pub const Metrics = struct {
    bytes_from_origin: std.atomic.Value(u64) = .init(0),
    bytes_to_client: std.atomic.Value(u64) = .init(0),
    cache_hits: std.atomic.Value(u64) = .init(0),
    cache_misses: std.atomic.Value(u64) = .init(0),
    prefetch_bytes: std.atomic.Value(u64) = .init(0),
    prefetch_cancelled: std.atomic.Value(u64) = .init(0),
};

const Slot = struct {
    valid: bool = false,
    block_index: u32 = 0,
    last_used: u64 = 0,
    filled_len: u32 = 0,
};

pub const BlockCache = struct {
    io: Io,
    origin: origin_mod.Origin,
    object_size: u64,
    storage: []u8,
    slots: [limits.CACHE_BLOCKS]Slot,
    mutex: Io.Mutex = .init,
    origin_sem: Io.Semaphore = .{ .permits = limits.MAX_CONCURRENT_ORIGIN_FILLS },
    clock: u64 = 1,
    cursor_block: u32 = 0,
    metrics: Metrics = .{},

    pub fn init(io: Io, origin: origin_mod.Origin, object_size: u64, storage: []u8) BlockCache {
        assert(storage.len == @as(usize, limits.CACHE_BLOCKS) * limits.BLOCK_SIZE);
        assert(object_size > 0);
        return .{
            .io = io,
            .origin = origin,
            .object_size = object_size,
            .storage = storage,
            .slots = .{Slot{}} ** limits.CACHE_BLOCKS,
        };
    }

    pub fn blockCount(self: *const BlockCache) u32 {
        const full = self.object_size / limits.BLOCK_SIZE;
        const rem = self.object_size % limits.BLOCK_SIZE;
        if (rem == 0) return @intCast(full);
        return @intCast(full + 1);
    }

    pub fn blockLen(self: *const BlockCache, block_index: u32) u32 {
        assert(block_index < self.blockCount());
        const start = @as(u64, block_index) * limits.BLOCK_SIZE;
        assert(start < self.object_size);
        const remaining = self.object_size - start;
        if (remaining >= limits.BLOCK_SIZE) return limits.BLOCK_SIZE;
        return @intCast(remaining);
    }

    pub fn occupancyBlocks(self: *BlockCache) u32 {
        self.mutex.lockUncancelable(self.io);
        defer self.mutex.unlock(self.io);
        var n: u32 = 0;
        for (self.slots) |slot| {
            if (slot.valid) n += 1;
        }
        assert(n <= limits.CACHE_BLOCKS);
        return n;
    }

    pub fn copyRange(self: *BlockCache, start: u64, end_inclusive: u64, out: *Io.Writer) !void {
        assert(start <= end_inclusive);
        assert(end_inclusive < self.object_size);
        const len = end_inclusive - start + 1;
        assert(len > 0);
        assert(len <= limits.MAX_RANGE_BYTES);

        var offset = start;
        var scratch: [limits.BLOCK_SIZE]u8 = undefined;
        while (offset <= end_inclusive) {
            const block_index: u32 = @intCast(offset / limits.BLOCK_SIZE);
            const block_start = @as(u64, block_index) * limits.BLOCK_SIZE;
            const local_off: usize = @intCast(offset - block_start);
            const want_end = @min(end_inclusive, block_start + self.blockLen(block_index) - 1);
            const local_end: usize = @intCast(want_end - block_start);
            assert(local_off <= local_end);
            const n = local_end - local_off + 1;

            try self.copyFromBlock(block_index, local_off, scratch[0..n]);
            try out.writeAll(scratch[0..n]);
            _ = self.metrics.bytes_to_client.fetchAdd(n, .monotonic);
            if (want_end == end_inclusive) break;
            offset = want_end + 1;
        }

        {
            try self.mutex.lock(self.io);
            defer self.mutex.unlock(self.io);
            const end_block: u32 = @intCast(end_inclusive / limits.BLOCK_SIZE);
            self.updateCursorLocked(end_block);
        }
    }

    pub fn copyRangeToSlice(self: *BlockCache, start: u64, end_inclusive: u64, out: []u8) !usize {
        assert(start <= end_inclusive);
        assert(end_inclusive < self.object_size);
        const len = end_inclusive - start + 1;
        assert(len > 0);
        assert(len <= limits.MAX_RANGE_BYTES);
        assert(out.len >= len);

        var offset = start;
        var written: usize = 0;
        while (offset <= end_inclusive) {
            const block_index: u32 = @intCast(offset / limits.BLOCK_SIZE);
            const block_start = @as(u64, block_index) * limits.BLOCK_SIZE;
            const local_off: usize = @intCast(offset - block_start);
            const want_end = @min(end_inclusive, block_start + self.blockLen(block_index) - 1);
            const local_end: usize = @intCast(want_end - block_start);
            assert(local_off <= local_end);
            const n = local_end - local_off + 1;

            try self.copyFromBlock(block_index, local_off, out[written..][0..n]);
            written += n;
            _ = self.metrics.bytes_to_client.fetchAdd(n, .monotonic);
            if (want_end == end_inclusive) break;
            offset = want_end + 1;
        }
        assert(written == len);

        {
            try self.mutex.lock(self.io);
            defer self.mutex.unlock(self.io);
            const end_block: u32 = @intCast(end_inclusive / limits.BLOCK_SIZE);
            self.updateCursorLocked(end_block);
        }
        return written;
    }

    pub fn prefetchAhead(self: *BlockCache) void {
        const cursor = blk: {
            self.mutex.lockUncancelable(self.io);
            defer self.mutex.unlock(self.io);
            break :blk self.cursor_block;
        };

        const total = self.blockCount();
        if (cursor + 1 >= total) return;

        var i: u32 = 1;
        while (i <= limits.PREFETCH_BLOCKS) : (i += 1) {
            const bi = cursor + i;
            if (bi >= total) break;
            const before_origin = self.metrics.bytes_from_origin.load(.monotonic);
            self.ensureBlockPresent(bi) catch {
                _ = self.metrics.prefetch_cancelled.fetchAdd(1, .monotonic);
                return;
            };
            const after_origin = self.metrics.bytes_from_origin.load(.monotonic);
            if (after_origin > before_origin) {
                _ = self.metrics.prefetch_bytes.fetchAdd(after_origin - before_origin, .monotonic);
            }
        }
    }

    fn updateCursorLocked(self: *BlockCache, end_block: u32) void {
        if (end_block > self.cursor_block + limits.PREFETCH_BLOCKS) {
            _ = self.metrics.prefetch_cancelled.fetchAdd(1, .monotonic);
        }
        self.cursor_block = end_block;
    }

    /// Copy `out.len` bytes from block at `local_off` into `out`.
    /// Never holds the cache mutex across origin I/O.
    fn copyFromBlock(self: *BlockCache, block_index: u32, local_off: usize, out: []u8) !void {
        assert(out.len > 0);
        try self.ensureBlockPresent(block_index);

        try self.mutex.lock(self.io);
        defer self.mutex.unlock(self.io);

        const slot_i = self.findSlot(block_index) orelse return error.CacheInconsistent;
        self.clock += 1;
        self.slots[slot_i].last_used = self.clock;
        const block = self.slotBytes(slot_i);
        assert(local_off + out.len <= block.len);
        @memcpy(out, block[local_off..][0..out.len]);
    }

    fn ensureBlockPresent(self: *BlockCache, block_index: u32) !void {
        assert(block_index < self.blockCount());

        {
            try self.mutex.lock(self.io);
            defer self.mutex.unlock(self.io);
            if (self.findSlot(block_index) != null) {
                _ = self.metrics.cache_hits.fetchAdd(1, .monotonic);
                return;
            }
            _ = self.metrics.cache_misses.fetchAdd(1, .monotonic);
        }

        const want = self.blockLen(block_index);
        var fill_buf: [limits.BLOCK_SIZE]u8 = undefined;
        const dest = fill_buf[0..want];
        const file_off = @as(u64, block_index) * limits.BLOCK_SIZE;

        try self.origin_sem.wait(self.io);
        defer self.origin_sem.post(self.io);
        try self.origin.readBlock(file_off, dest);

        try self.mutex.lock(self.io);
        defer self.mutex.unlock(self.io);

        // Another fiber may have filled this block while we fetched.
        if (self.findSlot(block_index) != null) return;

        const slot_i = self.evictOrFreeSlot();
        const slot_dest = self.storage[slot_i * limits.BLOCK_SIZE ..][0..want];
        @memcpy(slot_dest, dest);
        self.clock += 1;
        self.slots[slot_i] = .{
            .valid = true,
            .block_index = block_index,
            .last_used = self.clock,
            .filled_len = want,
        };
        _ = self.metrics.bytes_from_origin.fetchAdd(want, .monotonic);

        var occ: u32 = 0;
        for (self.slots) |s| {
            if (s.valid) occ += 1;
        }
        assert(occ <= limits.CACHE_BLOCKS);
        assert(occ > 0);
    }

    fn findSlot(self: *BlockCache, block_index: u32) ?usize {
        for (self.slots, 0..) |slot, i| {
            if (slot.valid and slot.block_index == block_index) return i;
        }
        return null;
    }

    fn evictOrFreeSlot(self: *BlockCache) usize {
        var free_i: ?usize = null;
        var lru_i: usize = 0;
        var lru_used: u64 = std.math.maxInt(u64);

        for (self.slots, 0..) |slot, i| {
            if (!slot.valid) {
                free_i = i;
                break;
            }
            if (slot.last_used < lru_used) {
                lru_used = slot.last_used;
                lru_i = i;
            }
        }

        if (free_i) |i| return i;
        assert(self.slots[lru_i].valid);
        self.slots[lru_i].valid = false;
        return lru_i;
    }

    fn slotBytes(self: *BlockCache, slot_i: usize) []const u8 {
        const slot = self.slots[slot_i];
        assert(slot.valid);
        assert(slot.filled_len > 0);
        assert(slot.filled_len <= limits.BLOCK_SIZE);
        return self.storage[slot_i * limits.BLOCK_SIZE ..][0..slot.filled_len];
    }
};

test "block count edges" {
    var storage: [limits.BLOCK_SIZE]u8 = undefined;
    var cache = BlockCache{
        .io = undefined,
        .origin = undefined,
        .object_size = limits.BLOCK_SIZE,
        .storage = storage[0..],
        .slots = .{Slot{}} ** limits.CACHE_BLOCKS,
    };
    try std.testing.expectEqual(@as(u32, 1), cache.blockCount());
    try std.testing.expectEqual(@as(u32, limits.BLOCK_SIZE), cache.blockLen(0));
    cache.object_size = limits.BLOCK_SIZE + 1;
    try std.testing.expectEqual(@as(u32, 2), cache.blockCount());
    try std.testing.expectEqual(@as(u32, 1), cache.blockLen(1));
}
