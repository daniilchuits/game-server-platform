(() => {
  "use strict";

  const POLL_INTERVAL_MS = 1500;
  const MAX_LOG_LINES = 600;
  const SHA256_PATTERN = /^[a-f0-9]{64}$/i;
  const elements = {
    connectionStatus: document.querySelector("#connectionStatus"),
    connectionText: document.querySelector("#connectionText"),
    serverStatus: document.querySelector("#serverStatus"),
    serverStatusText: document.querySelector("#serverStatusText"),
    serverVersion: document.querySelector("#serverVersion"),
    serverState: document.querySelector("#serverState"),
    startButton: document.querySelector("#startButton"),
    stopButton: document.querySelector("#stopButton"),
    serverError: document.querySelector("#serverError"),
    eulaPanel: document.querySelector("#eulaPanel"),
    eulaHeading: document.querySelector("#eula-heading"),
    eulaPath: document.querySelector("#eulaPath"),
    eulaForm: document.querySelector("#eulaForm"),
    eulaAccepted: document.querySelector("#eulaAccepted"),
    acceptEulaButton: document.querySelector("#acceptEulaButton"),
    logOutput: document.querySelector("#logOutput"),
    emptyLogs: document.querySelector("#emptyLogs"),
    autoScroll: document.querySelector("#autoScroll"),
    clearLogsButton: document.querySelector("#clearLogsButton"),
    commandForm: document.querySelector("#commandForm"),
    commandInput: document.querySelector("#commandInput"),
    sendCommandButton: document.querySelector("#sendCommandButton"),
    backupForm: document.querySelector("#backupForm"),
    backupMessage: document.querySelector("#backupMessage"),
    createBackupButton: document.querySelector("#createBackupButton"),
    refreshBackupsButton: document.querySelector("#refreshBackupsButton"),
    backupCount: document.querySelector("#backupCount"),
    backupLoading: document.querySelector("#backupLoading"),
    backupList: document.querySelector("#backupList"),
    toast: document.querySelector("#toast"),
    toastMessage: document.querySelector("#toastMessage"),
    dismissToastButton: document.querySelector("#dismissToastButton")
  };

  const view = {
    cursor: 0,
    state: "",
    running: false,
    busy: false,
    canStart: false,
    canStop: false,
    canSendCommands: false,
    eulaPending: false,
    actionPending: false,
    connected: false,
    backups: [],
    backupsLoading: false,
    polling: false,
    refetchLogs: false,
    pollTimer: null,
    toastTimer: null
  };

  async function request(path, options = {}) {
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }

    const response = await fetch(path, { ...options, headers });
    const rawBody = await response.text();
    let body = {};

    if (rawBody) {
      try {
        body = JSON.parse(rawBody);
      } catch {
        body = { message: rawBody };
      }
    }

    if (!response.ok) {
      const detail = body.error || body.message || `${response.status} ${response.statusText}`;
      throw new Error(detail);
    }

    return body;
  }

  function post(path, body) {
    return request(path, {
      method: "POST",
      body: JSON.stringify(body)
    });
  }

  function titleCase(value) {
    const text = String(value || "").trim();
    if (!text) {
      return "Unknown";
    }
    return text.charAt(0).toUpperCase() + text.slice(1);
  }

  function setConnection(connected) {
    view.connected = connected;
    setTone(elements.connectionStatus, connected ? "online" : "offline");
    setText(elements.connectionText, connected ? "Dashboard connected" : "Connection interrupted");
  }

  function showToast(message, tone = "info", timeout = 5000) {
    window.clearTimeout(view.toastTimer);
    elements.toastMessage.textContent = message;
    elements.toast.dataset.tone = tone;
    elements.toast.hidden = false;

    if (timeout > 0) {
      view.toastTimer = window.setTimeout(() => {
        elements.toast.hidden = true;
      }, timeout);
    }
  }

  function hideToast() {
    window.clearTimeout(view.toastTimer);
    elements.toast.hidden = true;
  }

  function displayError(error) {
    const message = error instanceof Error ? error.message : String(error);
    showToast(message || "The request could not be completed.", "error", 7000);
  }

  function updateControls() {
    const unavailable = view.actionPending || !view.canSendCommands || !view.connected;

    elements.startButton.disabled = view.actionPending || !view.canStart || !view.connected;
    elements.stopButton.disabled = view.actionPending || !view.canStop || !view.connected;
    elements.commandInput.disabled = unavailable;
    elements.sendCommandButton.disabled = unavailable;
    elements.backupMessage.disabled = unavailable;
    elements.createBackupButton.disabled = unavailable;
    elements.refreshBackupsButton.disabled = view.actionPending || view.backupsLoading || !view.connected;
    const canAnswerEula = view.eulaPending && view.canStop;
    elements.eulaAccepted.disabled = view.actionPending || !canAnswerEula || !view.connected;
    elements.acceptEulaButton.disabled = view.actionPending || !canAnswerEula || !view.connected || !elements.eulaAccepted.checked;

    updateRestoreControls();
  }

  function updateRestoreControls() {
    const restoreUnavailable = view.actionPending || !view.canSendCommands || !view.connected;
    const forms = elements.backupList.querySelectorAll(".restore-form");

    for (const form of forms) {
      const input = form.querySelector("input");
      const button = form.querySelector("button[type='submit']");
      const matches = input.value.trim().toLowerCase() === form.dataset.hash;
      input.disabled = restoreUnavailable;
      button.disabled = restoreUnavailable || !matches;
    }
  }

  function updateState(data) {
    const state = String(data.state || (data.running ? "running" : "stopped")).trim().toLowerCase();
    const previousState = view.state;
    const eulaWasPending = view.eulaPending;
    const responseCursor = Number(data.cursor);
    const cursorRolledBack = Number.isFinite(responseCursor) && responseCursor < view.cursor;

    if (cursorRolledBack) {
      view.cursor = 0;
      view.refetchLogs = true;
      resetLogView("Control panel restarted. Waiting for new server output…");
    }

    view.state = state;
    view.running = Boolean(data.running);
    view.eulaPending = Boolean(data.eula_pending);
    view.canStart = Boolean(data.can_start);
    view.canStop = Boolean(data.can_stop);
    view.canSendCommands = Boolean(data.can_send_commands);
    view.busy = view.running && !view.canSendCommands;

    setText(elements.serverVersion, data.version || "Not selected");
    setText(elements.serverState, titleCase(state));
    setText(elements.serverStatusText, titleCase(state));

    if (data.error) {
      setTone(elements.serverStatus, "error");
      setText(elements.serverError, String(data.error));
      elements.serverError.hidden = false;
    } else {
      elements.serverError.hidden = true;
      if (view.busy) {
        setTone(elements.serverStatus, "busy");
      } else if (view.running) {
        setTone(elements.serverStatus, "running");
      } else {
        setTone(elements.serverStatus, "stopped");
      }
    }

    elements.eulaPanel.hidden = !view.eulaPending;
    setText(elements.eulaPath, data.eula_path || "eula.txt");
    if (!view.eulaPending) {
      elements.eulaAccepted.checked = false;
    } else if (!eulaWasPending) {
      window.requestAnimationFrame(() => elements.eulaHeading.focus());
    }

    appendLogs(Array.isArray(data.logs) ? data.logs : []);

    if (!cursorRolledBack && Number.isFinite(responseCursor) && responseCursor >= view.cursor) {
      view.cursor = responseCursor;
    }

    if (previousState && previousState !== "running" && state === "running") {
      void refreshBackups();
    }

    updateControls();
  }

  function formatLogTime(value) {
    if (!value) {
      return "--:--:--";
    }

    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return String(value);
    }

    return new Intl.DateTimeFormat(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit"
    }).format(date);
  }

  function appendLogs(logs) {
    const unseen = logs.filter((log) => {
      const id = Number(log.id);
      return !Number.isFinite(id) || id > view.cursor;
    });
    if (unseen.length === 0) {
      return;
    }

    elements.emptyLogs.hidden = true;
    const fragment = document.createDocumentFragment();

    for (const log of unseen) {
      const row = document.createElement("div");
      row.className = "log-line";

      const time = document.createElement("time");
      time.className = "log-time";
      time.textContent = formatLogTime(log.time);
      if (log.time) {
        time.dateTime = String(log.time);
      }

      const source = document.createElement("span");
      source.className = "log-source";
      source.textContent = log.source || "server";
      if (/err(or)?|stderr/i.test(String(log.source || ""))) {
        source.dataset.tone = "error";
      }

      const message = document.createElement("span");
      message.className = "log-message";
      message.textContent = log.message || "";

      row.append(time, source, message);
      fragment.append(row);

      const id = Number(log.id);
      if (Number.isFinite(id) && id > view.cursor) {
        view.cursor = id;
      }
    }

    elements.logOutput.append(fragment);

    const lines = elements.logOutput.querySelectorAll(".log-line");
    const excess = lines.length - MAX_LOG_LINES;
    for (let index = 0; index < excess; index += 1) {
      lines[index].remove();
    }

    if (elements.autoScroll.checked) {
      elements.logOutput.scrollTop = elements.logOutput.scrollHeight;
    }
  }

  function resetLogView(message) {
    elements.logOutput.replaceChildren(elements.emptyLogs);
    elements.emptyLogs.hidden = false;
    setText(elements.emptyLogs, message);
  }

  function setText(element, value) {
    if (element.textContent !== value) {
      element.textContent = value;
    }
  }

  function setTone(element, value) {
    if (element.dataset.tone !== value) {
      element.dataset.tone = value;
    }
  }

  async function pollState() {
    if (view.polling) {
      return;
    }
    view.polling = true;
    window.clearTimeout(view.pollTimer);

    try {
      const data = await request(`/api/state?after=${encodeURIComponent(view.cursor)}`);
      setConnection(true);
      updateState(data);
    } catch {
      setConnection(false);
      updateControls();
    } finally {
      view.polling = false;
      const delay = view.refetchLogs ? 0 : POLL_INTERVAL_MS;
      view.refetchLogs = false;
      view.pollTimer = window.setTimeout(pollState, delay);
    }
  }

  async function runAction(button, pendingLabel, action) {
    if (view.actionPending) {
      return;
    }

    const originalLabel = button.textContent;
    view.actionPending = true;
    button.textContent = pendingLabel;
    updateControls();

    try {
      await action();
      await pollState();
    } catch (error) {
      displayError(error);
    } finally {
      view.actionPending = false;
      button.textContent = originalLabel;
      updateControls();
    }
  }

  function sendServerCommand(command) {
    return post("/api/command", { command });
  }

  function createTextElement(tagName, className, text) {
    const element = document.createElement(tagName);
    element.className = className;
    element.textContent = text;
    return element;
  }

  async function copyHash(hash) {
    try {
      await navigator.clipboard.writeText(hash);
      showToast("Backup hash copied.", "success", 2500);
    } catch {
      showToast("Copy is unavailable. Select the hash and copy it manually.", "error", 5000);
    }
  }

  function makeBackupItem(backup, index) {
    const name = String(backup.name || `Backup ${index + 1}`);
    const hash = String(backup.hash || "").trim().toLowerCase();
    const item = document.createElement("li");
    item.className = "backup-item";

    const header = document.createElement("div");
    header.className = "backup-item-header";
    const title = createTextElement("h4", "", name);
    const copyButton = createTextElement("button", "copy-button", "Copy hash");
    copyButton.type = "button";
    copyButton.setAttribute("aria-label", `Copy restore hash for ${name}`);
    copyButton.disabled = !SHA256_PATTERN.test(hash);
    copyButton.addEventListener("click", () => copyHash(hash));
    header.append(title, copyButton);

    const message = createTextElement("p", "backup-message", backup.message || "No message was added to this snapshot.");
    const hashText = createTextElement("code", "backup-hash", hash || "Restore hash unavailable");

    const form = document.createElement("form");
    form.className = "restore-form";
    form.dataset.hash = hash;

    const fieldID = `restore-hash-${index}`;
    const label = createTextElement("label", "", "Paste this hash to confirm restore");
    label.htmlFor = fieldID;

    const input = document.createElement("input");
    input.id = fieldID;
    input.type = "text";
    input.inputMode = "text";
    input.autocomplete = "off";
    input.spellcheck = false;
    input.maxLength = 64;
    input.pattern = "[A-Fa-f0-9]{64}";
    input.placeholder = "64-character restore hash";
    input.setAttribute("aria-label", `Confirmation hash for ${name}`);

    const restoreButton = createTextElement("button", "button button-danger", "Restore this backup");
    restoreButton.type = "submit";
    restoreButton.disabled = true;

    input.addEventListener("input", () => {
      input.setCustomValidity("");
      updateRestoreControls();
    });

    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const enteredHash = input.value.trim().toLowerCase();

      if (!SHA256_PATTERN.test(hash) || enteredHash !== hash) {
        input.setCustomValidity("Paste the exact restore hash shown above.");
        input.reportValidity();
        return;
      }

      const confirmed = window.confirm(`Restore “${name}”? The server will restart and the current world will be saved as a safety backup.`);
      if (!confirmed) {
        return;
      }

      runAction(restoreButton, "Restore requested…", async () => {
        await sendServerCommand(`backup use ${hash}`);
        input.value = "";
        showToast("Restore requested. Follow progress in the live console.", "success", 6500);
      });
    });

    form.append(label, input, restoreButton);
    item.append(header, message, hashText, form);
    return item;
  }

  function renderBackups(backups) {
    view.backups = backups;
    elements.backupList.replaceChildren();
    elements.backupCount.textContent = String(backups.length);

    if (backups.length === 0) {
      const empty = createTextElement("li", "backup-empty", "No completed backups yet.");
      elements.backupList.append(empty);
      return;
    }

    const fragment = document.createDocumentFragment();
    backups.forEach((backup, index) => fragment.append(makeBackupItem(backup, index)));
    elements.backupList.append(fragment);
    updateRestoreControls();
  }

  async function refreshBackups(showSuccess = false) {
    if (view.backupsLoading) {
      return;
    }
    view.backupsLoading = true;
    elements.backupLoading.hidden = false;
    elements.refreshBackupsButton.disabled = true;

    try {
      const data = await request("/api/backups");
      renderBackups(Array.isArray(data.backups) ? data.backups : []);
      if (showSuccess) {
        showToast("Backup list refreshed.", "success", 2500);
      }
    } catch (error) {
      displayError(error);
    } finally {
      view.backupsLoading = false;
      elements.backupLoading.hidden = true;
      updateControls();
    }
  }

  elements.startButton.addEventListener("click", () => {
    runAction(elements.startButton, "Starting…", async () => {
      await post("/api/start", {});
      showToast("Server start requested.", "success", 3500);
    });
  });

  elements.stopButton.addEventListener("click", () => {
    runAction(elements.stopButton, "Stopping…", async () => {
      await post("/api/stop", {});
      showToast("Server stop requested.", "success", 3500);
    });
  });

  elements.eulaAccepted.addEventListener("change", updateControls);

  elements.eulaForm.addEventListener("submit", (event) => {
    event.preventDefault();
    if (!elements.eulaAccepted.checked) {
      return;
    }

    runAction(elements.acceptEulaButton, "Accepting…", async () => {
      await post("/api/eula", { accepted: true });
      showToast("EULA accepted. Server setup is continuing.", "success", 5000);
    });
  });

  elements.commandForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const command = elements.commandInput.value.trim();
    if (!command) {
      elements.commandInput.focus();
      return;
    }

    runAction(elements.sendCommandButton, "Sending…", async () => {
      await sendServerCommand(command);
      elements.commandInput.value = "";
      elements.commandInput.focus();
    });
  });

  elements.backupForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const message = elements.backupMessage.value.trim();
    const command = message ? `backup create ${message}` : "backup create";

    runAction(elements.createBackupButton, "Requesting…", async () => {
      await sendServerCommand(command);
      elements.backupMessage.value = "";
      showToast("Backup requested. Follow progress in the live console.", "success", 6000);
    });
  });

  elements.refreshBackupsButton.addEventListener("click", () => refreshBackups(true));

  elements.clearLogsButton.addEventListener("click", () => {
    resetLogView("Log view cleared. New output will appear here.");
  });

  elements.dismissToastButton.addEventListener("click", hideToast);

  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      pollState();
    }
  });

  window.addEventListener("beforeunload", () => {
    window.clearTimeout(view.pollTimer);
    window.clearTimeout(view.toastTimer);
  });

  pollState();
  refreshBackups();
})();
