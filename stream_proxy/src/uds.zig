const std = @import("std");
const Io = std.Io;
const net = Io.net;
const limits = @import("limits.zig");
const proxy = @import("proxy.zig");
const spch = @import("spch.zig");

pub fn servePath(state: *proxy.State, uds_path: []const u8) !void {
    // Stale socket from a prior crash blocks listen.
    Io.Dir.deleteFileAbsolute(state.io, uds_path) catch |err| switch (err) {
        error.FileNotFound => {},
        else => return err,
    };

    const address = try net.UnixAddress.init(uds_path);
    var server = try address.listen(state.io, .{
        .kernel_backlog = limits.LISTEN_BACKLOG,
    });
    defer {
        server.deinit(state.io);
        Io.Dir.deleteFileAbsolute(state.io, uds_path) catch {};
    }

    std.log.info("stream_proxy uds listening on {s}", .{uds_path});

    var group: Io.Group = .init;
    defer group.cancel(state.io);

    while (true) {
        const stream = server.accept(state.io) catch |err| switch (err) {
            error.Canceled => return,
            else => {
                std.log.err("uds accept failed: {t}", .{err});
                continue;
            },
        };
        if (!state.tryAcquireConnection()) {
            var copy = stream;
            copy.close(state.io);
            std.log.warn("connection limit reached ({d}); dropped UDS client", .{limits.MAX_CONNECTIONS});
            continue;
        }
        group.concurrent(state.io, spch.handleConnection, .{ state, stream }) catch {
            state.releaseConnection();
            var copy = stream;
            copy.close(state.io);
            std.log.err("failed to spawn uds handler", .{});
        };
    }
}
