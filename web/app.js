// AIDaemon Chat — minimal WebSocket chat client.
(function () {
  "use strict";

  const STORAGE_KEY = "aidaemon_session_id";
  const TOKEN_KEY = "aidaemon_token";
  const RECONNECT_DELAY = 2000;
  const MAX_RECONNECT_DELAY = 30000;

  let ws = null;
  let currentSessionId = localStorage.getItem(STORAGE_KEY) || newSessionId();
  let reconnectDelay = RECONNECT_DELAY;
  let reconnectTimer = null;
  let pendingReply = false;

  // DOM elements.
  const messagesEl = document.getElementById("messages");
  const inputEl = document.getElementById("input");
  const sendBtn = document.getElementById("send");
  const statusEl = document.getElementById("status");
  const sessionListEl = document.getElementById("session-list");
  const newSessionBtn = document.getElementById("new-session");

  function newSessionId() {
    const id = "web-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
    localStorage.setItem(STORAGE_KEY, id);
    currentSessionId = id;
    return id;
  }

  function getToken() {
    return localStorage.getItem(TOKEN_KEY) || "";
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
      loadSessions();
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

      // Handle session rotation.
      if (data.type === 'session_rotated') {
        currentSessionId = data.session_id;
        localStorage.setItem(STORAGE_KEY, currentSessionId);
        clearMessages();
        loadSessions();
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

  // --- Session Management ---

  async function loadSessions() {
    try {
      const token = getToken();
      const headers = {};
      if (token) {
        headers['Authorization'] = 'Bearer ' + token;
      }
      
      const resp = await fetch('/sessions', { headers });
      if (!resp.ok) {
        console.error('Failed to load sessions:', resp.status);
        return;
      }
      
      const sessions = await resp.json();
      renderSessions(sessions);
    } catch (err) {
      console.error('Error loading sessions:', err);
    }
  }

  function renderSessions(sessions) {
    if (!sessions || sessions.length === 0) {
      sessionListEl.innerHTML = '<div style="padding: 12px; text-align: center; color: var(--text-muted); font-size: 12px;">No sessions yet</div>';
      return;
    }

    sessionListEl.innerHTML = sessions.map(function(s) {
      const isActive = s.id === currentSessionId;
      const title = s.title || 'Untitled';
      const date = new Date(s.created_at).toLocaleDateString();
      const statusClass = s.status === 'archived' ? 'archived' : '';
      const activeClass = isActive ? 'active' : '';
      
      return '<div class="session-item ' + statusClass + ' ' + activeClass + '" data-session-id="' + s.id + '">' +
        '<span class="title">' + escapeHtml(title) + '</span>' +
        '<span class="date">' + date + '</span>' +
        '</div>';
    }).join('');

    // Add click handlers.
    sessionListEl.querySelectorAll('.session-item').forEach(function(el) {
      el.addEventListener('click', function() {
        switchSession(el.getAttribute('data-session-id'));
      });
    });
  }

  async function switchSession(id) {
    if (id === currentSessionId) return;

    try {
      const token = getToken();
      const headers = {};
      if (token) {
        headers['Authorization'] = 'Bearer ' + token;
      }

      const resp = await fetch('/sessions/' + id + '/messages', { headers });
      if (!resp.ok) {
        addMessage('error', 'Failed to load session messages');
        return;
      }

      const messages = await resp.json();
      
      // Switch session.
      currentSessionId = id;
      localStorage.setItem(STORAGE_KEY, id);
      
      // Clear and reload messages.
      clearMessages();
      messages.forEach(function(msg) {
        if (msg.role === 'user' || msg.role === 'assistant') {
          addMessage(msg.role, msg.content);
        }
      });

      // Update sidebar highlight.
      loadSessions();
    } catch (err) {
      console.error('Error switching session:', err);
      addMessage('error', 'Failed to switch session');
    }
  }

  function createNewSession() {
    // Send /new command via WebSocket.
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      addMessage('error', 'Not connected');
      return;
    }

    ws.send(JSON.stringify({
      type: 'command',
      command: 'new',
      session_id: currentSessionId
    }));
  }

  function handleCommand(text) {
    if (text === '/new') {
      createNewSession();
      return true;
    }
    
    if (text.startsWith('/title ')) {
      const title = text.slice(7).trim();
      if (!title) return false;
      
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        addMessage('error', 'Not connected');
        return true;
      }

      ws.send(JSON.stringify({
        type: 'command',
        command: 'title',
        text: title,
        session_id: currentSessionId
      }));
      
      addMessage('user', text);
      inputEl.value = '';
      autoResize();
      
      return true;
    }
    
    return false;
  }

  function escapeHtml(text) {
    var div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  function clearMessages() {
    messagesEl.innerHTML = '';
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

    // Check if this is a command.
    if (handleCommand(text)) {
      return;
    }

    addMessage("user", text);
    inputEl.value = "";
    autoResize();

    ws.send(JSON.stringify({
      message: text,
      session_id: currentSessionId,
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

  newSessionBtn.addEventListener("click", createNewSession);

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
