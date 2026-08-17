use gpui::{
    App, Bounds, Context, Div, FontWeight, IntoElement, Render, Window, WindowBounds,
    WindowOptions, div, prelude::*, px, rgb, size,
};

const APP_BACKGROUND: u32 = 0x0c111b;
const SIDEBAR_BACKGROUND: u32 = 0x111926;
const SURFACE: u32 = 0x151f2e;
const SURFACE_HOVER: u32 = 0x1a2638;
const SURFACE_SELECTED: u32 = 0x1b2d43;
const BORDER: u32 = 0x27364b;
const TEXT: u32 = 0xe7edf7;
const TEXT_MUTED: u32 = 0x8a9ab0;
const ACCENT: u32 = 0x61b6ff;
const SUCCESS: u32 = 0x53d69b;
const WARNING: u32 = 0xf1b65c;

#[derive(Clone, Copy)]
struct MountPreview {
    name: &'static str,
    drive: &'static str,
    connected: bool,
}

const MOUNTS: [MountPreview; 3] = [
    MountPreview {
        name: "Personal",
        drive: "Z:",
        connected: true,
    },
    MountPreview {
        name: "Work archive",
        drive: "—",
        connected: false,
    },
    MountPreview {
        name: "College",
        drive: "—",
        connected: false,
    },
];

struct DesktopApp {
    selected_mount: usize,
}

impl DesktopApp {
    fn new() -> Self {
        Self { selected_mount: 0 }
    }

    fn select_mount(&mut self, index: usize, cx: &mut Context<Self>) {
        self.selected_mount = index;
        cx.notify();
    }

    fn mount_row(
        selected_mount: usize,
        mount: MountPreview,
        index: usize,
        cx: &mut Context<Self>,
    ) -> impl IntoElement {
        let selected = selected_mount == index;
        let status_color = if mount.connected {
            rgb(SUCCESS)
        } else {
            rgb(TEXT_MUTED)
        };
        let surface = if selected {
            rgb(SURFACE_SELECTED)
        } else {
            rgb(SIDEBAR_BACKGROUND)
        };

        div()
            .id(("mount", index))
            .w_full()
            .px_3()
            .py_2()
            .rounded_md()
            .cursor_pointer()
            .bg(surface)
            .hover(move |style| style.bg(rgb(SURFACE_HOVER)))
            .on_click(cx.listener(move |this, _, _, cx| this.select_mount(index, cx)))
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .min_w_0()
                            .child(div().size_2().rounded_full().bg(status_color))
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(rgb(TEXT))
                                    .overflow_hidden()
                                    .text_ellipsis()
                                    .child(mount.name),
                            ),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(rgb(TEXT_MUTED))
                            .flex_none()
                            .child(mount.drive),
                    ),
            )
            .child(
                div()
                    .pl_4()
                    .mt_1()
                    .text_xs()
                    .text_color(status_color)
                    .child(if mount.connected {
                        "Connected"
                    } else {
                        "Disconnected"
                    }),
            )
    }

    fn panel(&self, title: &'static str, detail: &'static str, content: impl IntoElement) -> Div {
        div()
            .flex()
            .flex_col()
            .gap_4()
            .p_5()
            .rounded_lg()
            .bg(rgb(SURFACE))
            .border_1()
            .border_color(rgb(BORDER))
            .child(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(
                        div()
                            .text_sm()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(rgb(TEXT))
                            .child(title),
                    )
                    .child(div().text_xs().text_color(rgb(TEXT_MUTED)).child(detail)),
            )
            .child(content)
    }
}

impl Render for DesktopApp {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let mount = MOUNTS[self.selected_mount];
        let status_text = if mount.connected {
            "Connected"
        } else {
            "Disconnected"
        };
        let status_color = if mount.connected {
            rgb(SUCCESS)
        } else {
            rgb(WARNING)
        };

        div()
            .id("infinity-desktop")
            .size_full()
            .flex()
            .bg(rgb(APP_BACKGROUND))
            .font_family("Segoe UI")
            .child(
                div()
                    .w(px(272.))
                    .h_full()
                    .flex_none()
                    .flex()
                    .flex_col()
                    .px_3()
                    .py_4()
                    .bg(rgb(SIDEBAR_BACKGROUND))
                    .border_r_1()
                    .border_color(rgb(BORDER))
                    .child(
                        div()
                            .px_2()
                            .pb_5()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(
                                div()
                                    .size_7()
                                    .rounded_md()
                                    .bg(rgb(ACCENT))
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_color(rgb(APP_BACKGROUND))
                                    .font_weight(FontWeight::BLACK)
                                    .child("I"),
                            )
                            .child(
                                div()
                                    .flex()
                                    .flex_col()
                                    .child(
                                        div()
                                            .text_base()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(rgb(TEXT))
                                            .child("Infinity Storage"),
                                    )
                                    .child(
                                        div()
                                            .text_xs()
                                            .text_color(rgb(TEXT_MUTED))
                                            .child("Desktop preview"),
                                    ),
                            ),
                    )
                    .child(
                        div()
                            .px_2()
                            .pb_2()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .text_xs()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(rgb(TEXT_MUTED))
                                    .child("YOUR MOUNTS"),
                            )
                            .child(
                                div()
                                    .size_5()
                                    .rounded_sm()
                                    .border_1()
                                    .border_color(rgb(BORDER))
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .text_color(rgb(ACCENT))
                                    .text_sm()
                                    .cursor_pointer()
                                    .hover(|style| style.bg(rgb(SURFACE_HOVER)))
                                    .child("+"),
                            ),
                    )
                    .child(
                        div()
                            .flex()
                            .flex_col()
                            .gap_1()
                            .child(Self::mount_row(self.selected_mount, MOUNTS[0], 0, cx))
                            .child(Self::mount_row(self.selected_mount, MOUNTS[1], 1, cx))
                            .child(Self::mount_row(self.selected_mount, MOUNTS[2], 2, cx)),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .mt_4()
                            .p_3()
                            .rounded_md()
                            .bg(rgb(SURFACE))
                            .border_1()
                            .border_color(rgb(BORDER))
                            .child(div().text_xs().text_color(rgb(TEXT_MUTED)).child(
                                "Mount actions remain local UI previews until backend integration.",
                            )),
                    ),
            )
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .flex()
                    .flex_col()
                    .p_8()
                    .gap_7()
                    .child(
                        div()
                            .flex()
                            .items_start()
                            .justify_between()
                            .child(
                                div()
                                    .flex()
                                    .flex_col()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_xs()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(rgb(ACCENT))
                                            .child("MOUNT OVERVIEW"),
                                    )
                                    .child(
                                        div()
                                            .text_2xl()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(rgb(TEXT))
                                            .child(mount.name),
                                    )
                                    .child(
                                        div()
                                            .text_sm()
                                            .text_color(rgb(TEXT_MUTED))
                                            .child("Drive and bucket controls will connect here."),
                                    ),
                            )
                            .child(
                                div()
                                    .px_3()
                                    .py_2()
                                    .rounded_full()
                                    .bg(rgb(SURFACE))
                                    .border_1()
                                    .border_color(rgb(BORDER))
                                    .flex()
                                    .items_center()
                                    .gap_2()
                                    .child(div().size_2().rounded_full().bg(status_color))
                                    .child(
                                        div()
                                            .text_sm()
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(status_color)
                                            .child(status_text),
                                    ),
                            ),
                    )
                    .child(
                        self.panel(
                            "Drive assignment",
                            "Windows or Linux mount target",
                            div()
                                .flex()
                                .items_end()
                                .justify_between()
                                .child(
                                    div()
                                        .flex()
                                        .flex_col()
                                        .gap_1()
                                        .child(
                                            div()
                                                .text_3xl()
                                                .font_weight(FontWeight::SEMIBOLD)
                                                .text_color(rgb(TEXT))
                                                .child(mount.drive),
                                        )
                                        .child(div().text_xs().text_color(rgb(TEXT_MUTED)).child(
                                            if mount.connected {
                                                "Assigned drive letter"
                                            } else {
                                                "Drive letter assigned on connect"
                                            },
                                        )),
                                )
                                .child(
                                    div()
                                        .px_3()
                                        .py_2()
                                        .rounded_md()
                                        .border_1()
                                        .border_color(rgb(BORDER))
                                        .text_sm()
                                        .text_color(rgb(TEXT_MUTED))
                                        .child("Manage"),
                                ),
                        ),
                    ),
            )
            .child(
                div()
                    .flex()
                    .gap_5()
                    .child(
                        self.panel(
                            "Connection",
                            "Mount process",
                            div()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(div().size_2().rounded_full().bg(status_color))
                                .child(div().text_sm().text_color(rgb(TEXT)).child(status_text)),
                        )
                        .flex_1(),
                    )
                    .child(
                        self.panel(
                            "Storage source",
                            "S3 bucket",
                            div()
                                .text_sm()
                                .text_color(rgb(TEXT_MUTED))
                                .child("Bucket setup pending"),
                        )
                        .flex_1(),
                    ),
            )
            .child(
                self.panel(
                    "Activity",
                    "Recent mount events",
                    div()
                        .flex()
                        .items_center()
                        .justify_between()
                        .py_2()
                        .border_t_1()
                        .border_color(rgb(BORDER))
                        .child(
                            div()
                                .text_sm()
                                .text_color(rgb(TEXT_MUTED))
                                .child("Events appear after mount service is connected."),
                        )
                        .child(div().text_xs().text_color(rgb(TEXT_MUTED)).child("Preview")),
                ),
            )
    }
}

fn main() {
    gpui_platform::application().run(|cx: &mut App| {
        let bounds = Bounds::centered(None, size(px(1180.), px(760.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |_, cx| cx.new(|_| DesktopApp::new()),
        )
        .expect("open Infinity Storage window");
    });
}
