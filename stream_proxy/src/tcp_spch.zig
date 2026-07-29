const std = @import("std");
const Io = std.Io;
const net = Io.net;
const limits = @import("limits.zig");
const proxy = @import("proxy.zig");
const spch = @import("spch.zig");

/// Serve SPCH over TCP at host:port (e.g. "127.0.0.1:0" for ephemeral).
pub fn serveAddr(state: *proxy.State, addr_text: []const u8) !void {
    const address = try net.IpAddress.parseLiteral(addr_text);
    var server = try address.listen(state.io, .{
        .reuse_address = true,
        .kernel_backlog = limits.LISTEN_BACKLOG,
    });
    defer server.deinit(state.io);

    std.log.info("stream_proxy spch-tcp listening on {f}", .{server.socket.address});

    var group: Io.Group = .init;
    defer group.cancel(state.io);

    while (true) {
        const stream = server.accept(state.io) catch |err| switch (err) {
            error.Canceled => return,
            else => {
                std.log.err("spch-tcp accept failed: {t}", .{err});
                continue;
            },
        };
        if (!state.tryAcquireConnection()) {
            var copy = stream;
            copy.close(state.io);
            std.log.warn("connection limit reached ({d}); dropped TCP SPCH client", .{limits.MAX_CONNECTIONS});
            continue;
        }
        group.concurrent(state.io, spch.handleConnection, .{ state, stream }) catch {
            state.releaseConnection();
            var copy = stream;
            copy.close(state.io);
            std.log.err("failed to spawn spch-tcp handler", .{});
        };
    }
}
