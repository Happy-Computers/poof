"use strict";

const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("infinity_storage", {
    auth_session: () => ipcRenderer.invoke("infinity_storage:auth_session"),
    auth_sign_up: (credentials) => ipcRenderer.invoke("infinity_storage:auth_sign_up", credentials),
    auth_sign_in: (credentials) => ipcRenderer.invoke("infinity_storage:auth_sign_in", credentials),
    auth_sign_out: () => ipcRenderer.invoke("infinity_storage:auth_sign_out"),
    auth_reset: (email) => ipcRenderer.invoke("infinity_storage:auth_reset", email),
    get_status: () => ipcRenderer.invoke("infinity_storage:get_status"),
    mount: (options) => ipcRenderer.invoke("infinity_storage:mount", options),
    unmount: () => ipcRenderer.invoke("infinity_storage:unmount"),
    open_folder: (mount_path) => ipcRenderer.invoke("infinity_storage:open_folder", mount_path),
    on_status: (callback) => {
        const listener = (_event, payload) => {
            callback(payload);
        };
        ipcRenderer.on("infinity_storage:status", listener);
        return () => {
            ipcRenderer.removeListener("infinity_storage:status", listener);
        };
    },
});
