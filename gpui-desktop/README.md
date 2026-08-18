# Infinity Storage desktop

GPUI desktop UI prototype for Windows and Linux. Contains no mount process, S3, persistence, or backend logic.

## GPUI source

Dependencies resolve only from local GPUI-CE checkout at `../../gpui-ce`. Required path:

```text
/home/amaan/projects/gpui-ce
```

## Linux

Install native build dependencies before running Cargo:

```bash
sudo apt update
sudo apt install -y build-essential pkg-config libfontconfig1-dev libfreetype6-dev libxcb1-dev libxkbcommon-dev libxkbcommon-x11-dev
cd gpui-desktop
cargo run
```

GPUI-CE selects Wayland and X11 on Linux.

## Windows

Do not build from `\\wsl.localhost\...`. Rust incremental compilation cannot lock its session directory on this filesystem.

Permanent setup: copy both repositories to a Windows-local directory while preserving their sibling layout:

```text
C:\dev\projects\Stuff\gpui-desktop
C:\dev\projects\gpui-ce
```

Then run from Windows PowerShell:

```powershell
cd C:\dev\projects\Stuff\gpui-desktop
cargo run
```

Temporary WSL-source workaround:

```powershell
$env:CARGO_INCREMENTAL = "0"
$env:CARGO_TARGET_DIR = "$env:LOCALAPPDATA\InfinityStorage\target"
cargo run --manifest-path "\\wsl.localhost\Ubuntu-24.04\home\amaan\projects\Stuff\gpui-desktop\Cargo.toml"
```

Install Rust MSVC toolchain and Visual Studio Build Tools with Desktop development with C++ if Cargo later reports a missing linker. GPUI-CE selects Win32 on Windows.
