//! Pluggable origin for BlockCache fills (local file or HTTP Range).

const std = @import("std");
const Io = std.Io;
const File = Io.File;
const http = std.http;
const assert = std.debug.assert;
const limits = @import("limits.zig");

pub const Origin = struct {
    ptr: *anyopaque,
    vtable: *const VTable,

    pub const VTable = struct {
        /// Fill `dest` with exactly `dest.len` bytes starting at `offset`.
        readBlock: *const fn (ptr: *anyopaque, offset: u64, dest: []u8) anyerror!void,
    };

    pub fn readBlock(self: Origin, offset: u64, dest: []u8) !void {
        assert(dest.len > 0);
        assert(dest.len <= limits.BLOCK_SIZE);
        return self.vtable.readBlock(self.ptr, offset, dest);
    }
};

pub const FileOrigin = struct {
    io: Io,
    file: File,

    pub fn origin(self: *FileOrigin) Origin {
        return .{
            .ptr = self,
            .vtable = &.{
                .readBlock = readBlock,
            },
        };
    }

    fn readBlock(ptr: *anyopaque, offset: u64, dest: []u8) anyerror!void {
        const self: *FileOrigin = @ptrCast(@alignCast(ptr));
        const got = try self.file.readPositionalAll(self.io, dest, offset);
        if (got != dest.len) return error.UnexpectedEof;
    }
};

pub const HttpOrigin = struct {
    io: Io,
    client: *http.Client,
    /// Externally owned; must outlive this origin (e.g. arena strdup of --origin-url).
    url: []const u8,

    pub fn origin(self: *HttpOrigin) Origin {
        return .{
            .ptr = self,
            .vtable = &.{
                .readBlock = readBlock,
            },
        };
    }

    /// HEAD the origin URL; returns Content-Length.
    pub fn headSize(self: *HttpOrigin) !u64 {
        const uri = try std.Uri.parse(self.url);
        var req = try self.client.request(.HEAD, uri, .{
            .redirect_behavior = .unhandled,
        });
        defer req.deinit();
        try req.sendBodiless();
        var redirect_buf: [256]u8 = undefined;
        const response = try req.receiveHead(&redirect_buf);
        if (response.head.status != .ok) return error.OriginHeadFailed;
        const len = response.head.content_length orelse return error.MissingContentLength;
        if (len == 0) return error.EmptyOrigin;
        return len;
    }

    fn readBlock(ptr: *anyopaque, offset: u64, dest: []u8) anyerror!void {
        const self: *HttpOrigin = @ptrCast(@alignCast(ptr));
        assert(dest.len > 0);
        const end_inclusive = offset + dest.len - 1;

        var range_buf: [64]u8 = undefined;
        const range_value = try std.fmt.bufPrint(&range_buf, "bytes={d}-{d}", .{ offset, end_inclusive });

        const uri = try std.Uri.parse(self.url);
        var req = try self.client.request(.GET, uri, .{
            .redirect_behavior = .unhandled,
            .extra_headers = &.{
                .{ .name = "range", .value = range_value },
            },
        });
        defer req.deinit();
        try req.sendBodiless();

        var redirect_buf: [256]u8 = undefined;
        var response = try req.receiveHead(&redirect_buf);
        if (response.head.status != .partial_content) return error.OriginRangeFailed;

        var transfer_buf: [8192]u8 = undefined;
        const body_reader = response.reader(&transfer_buf);
        try body_reader.readSliceAll(dest);
    }
};
