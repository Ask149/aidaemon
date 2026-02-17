// AIDaemon Chat — minimal WebSocket chat client.
(function () {
  "use strict";

  const STORAGE_KEY = "aidaemon_session_id";
  const RECONNECT_DELAY = 2000;
  const MAX_RECONNECT_DELAY = 30000;

  let ws = null;
  let sessionId = localStorage.getItem(STORAGE_KEY) || newSessionId();
  let reconnectDelay = RECONNECT_DELAY;
  let reconnectTimer = null;
  let pendingReply = false;

  // DOM elements.
  const messagesEl = document.getElementById("messages");
  const inputEl = document.getElementById("input");
  const sendBtn = document.getElementById("send");
  const statusEl = document.getElementById("status");

  function newSessionId() {
    const id = "web-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
    localStorage.setItem(STORAGE_KEY, id);
    return id;
  }

  // --- WebSocket ---

  function connect() {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = proto + "//" + location.host + "/ws";

    statusEl.textContent = "connecting...";
    statusEl.className = "";

    ws = new WebSocket(url);

    ws.onopen = function () {
      statusEl.textContent = "connected";
      statusEl.className = "connected";
      reconnectDelay = RECONNECT_DELAY;
    };

    ws.onclose = function () {
      statusEl.textContent = "disconnected";
      statusEl.className = "error";
      scheduleReconnect();
      if (pendingReply) {
        removeThinking();
        pendingReply = false;
        setInputEnabled(true);
      }
    };

    ws.onerror = function () {
      statusEl.textContent = "error";
      statusEl.className = "error";
    };

    ws.onmessage = function (evt) {
      var data;
      try {
        data = JSON.parse(evt.data);
      } catch (e) {
        return;
      }

      // Image messages don't clear the thinking indicator — more may follow.
      if (data.image) {
        addImage(data.image);
        return;
      }

      removeThinking();
      pendingReply = false;
      setInputEnabled(true);

      if (data.error) {
        addMessage("error", data.error);
      } else if (data.reply) {
        addMessage("assistant", data.reply);
      }
    };
  }

  function scheduleReconnect() {
    if (reconnectTimer) return;
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY);
  }

  // --- UI ---

  function addMessage(role, text) {
    var el = document.createElement("div");
    el.className = "msg " + role;
    el.textContent = text;
    messagesEl.appendChild(el);
    messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  function addImage(dataURL) {
    var el = document.createElement("div");
    el.className = "msg assistant image-msg";
    var img = document.createElement("img");
    img.src = dataURL;
    img.alt = "Screenshot";
    img.onclick = function () { window.open(dataURL, "_blank"); };
    el.appendChild(img);
    messagesEl.appendChild(el);
    messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  function addThinking() {
    var el = document.createElement("div");
    el.className = "msg thinking";
    el.id = "thinking";
    el.textContent = "thinking...";
    messagesEl.appendChild(el);
    messagesEl.scrollTop = messagesEl.scrollHeight;
  }

  function removeThinking() {
    var el = document.getElementById("thinking");
    if (el) el.remove();
  }

  function setInputEnabled(enabled) {
    inputEl.disabled = !enabled;
    sendBtn.disabled = !enabled;
    if (enabled) inputEl.focus();
  }

  function sendMessage() {
    var text = inputEl.value.trim();
    if (!text) return;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      addMessage("error", "Not connected. Reconnecting...");
      connect();
      return;
    }

    addMessage("user", text);
    inputEl.value = "";
    autoResize();

    ws.send(JSON.stringify({
      message: text,
      session_id: sessionId,
    }));

    pendingReply = true;
    setInputEnabled(false);
    addThinking();
  }

  // Auto-resize textarea.
  function autoResize() {
    inputEl.style.height = "auto";
    inputEl.style.height = Math.min(inputEl.scrollHeight, 120) + "px";
  }

  // --- Events ---

  sendBtn.addEventListener("click", sendMessage);

  inputEl.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  });

  inputEl.addEventListener("input", autoResize);

  // Start.
  connect();
  inputEl.focus();
})();
