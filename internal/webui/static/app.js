(() => {
  const state = {
    spaces: [], current: null, captchaId: "", viewVersion: 0, sessionVersion: 0, historyController: null,
    pendingSpaces: new Map(),
  };
  const el = (id) => document.getElementById(id);

  async function api(path, options = {}) {
    const response = await fetch(path, {
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", ...(options.headers || {}) },
      ...options,
    });
    if (response.status === 204) return null;
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(data.error?.message || "请求失败");
      error.code = data.error?.code || "request_failed";
      error.status = response.status;
      throw error;
    }
    return data;
  }

  async function boot() {
    try {
      await api("/api/v1/auth/session");
      await showChat();
    } catch (error) {
      if (error.status !== 401) el("login-error").textContent = "暂时无法连接服务器";
      await showLogin();
    }
  }

  async function showLogin() {
    state.sessionVersion += 1;
    state.viewVersion += 1;
    state.historyController?.abort();
    state.current = null;
    state.pendingSpaces.clear();
    el("chat-app").hidden = true;
    el("login-panel").hidden = false;
    await refreshCaptcha();
  }

  async function refreshCaptcha() {
    try {
      const data = await api("/api/v1/auth/captcha");
      state.captchaId = data.captcha_id;
      el("captcha-image").src = data.image;
      el("captcha-answer").value = "";
    } catch (_) {
      el("login-error").textContent = "验证码暂时不可用";
    }
  }

  async function showChat() {
    state.sessionVersion += 1;
    state.pendingSpaces.clear();
    el("login-panel").hidden = true;
    el("chat-app").hidden = false;
    const data = await api("/api/v1/conversations");
    state.spaces = data.conversations || [];
    renderSpaces();
    if (state.spaces.length) await selectSpace(state.spaces[0].id);
  }

  function renderSpaces() {
    const list = el("conversation-list");
    list.replaceChildren();
    state.spaces.forEach((space) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "conversation-button";
      button.dataset.conversationId = space.id;
      button.setAttribute("aria-label", `打开${space.display_name}`);
      const avatar = document.createElement("img");
      avatar.src = space.participants[0]?.avatar_url || "";
      avatar.alt = "";
      const copy = document.createElement("span");
      const title = document.createElement("strong");
      title.textContent = space.display_name;
      const detail = document.createElement("small");
      detail.textContent = space.kind === "group" ? "5 位团员" : "单独聊天";
      copy.append(title, detail);
      button.append(avatar, copy);
      button.addEventListener("click", () => selectSpace(space.id));
      list.append(button);
    });
  }

  async function selectSpace(id) {
    const space = state.spaces.find((item) => item.id === id);
    if (!space) return;
    if (state.current?.id !== id) el("message-input").value = "";
    state.current = space;
    state.viewVersion += 1;
    const version = state.viewVersion;
    state.historyController?.abort();
    state.historyController = new AbortController();
    el("conversation-title").textContent = space.display_name;
    el("participant-names").textContent = space.participants.map((item) => item.display_name).join(" · ");
    el("message-input").placeholder = space.kind === "group" ? "对 SOS 团说点什么……" : `对${space.display_name}说点什么……`;
    renderPendingState();
    document.querySelectorAll(".conversation-button").forEach((button) => {
      button.classList.toggle("active", button.dataset.conversationId === id);
    });
    el("messages").replaceChildren();
    hideNotice();
    try {
      const data = await api(`/api/v1/conversations/${encodeURIComponent(id)}/messages`, { signal: state.historyController.signal });
      if (version === state.viewVersion && state.current?.id === id) renderMessages(data.messages || []);
    } catch (error) {
      if (error.name !== "AbortError" && version === state.viewVersion) showNotice(error.message, "error");
    }
  }

  function renderMessages(messages) {
    const transcript = el("messages");
    transcript.replaceChildren();
    messages.forEach((message) => {
      const article = document.createElement("article");
      article.className = `message ${message.speaker_kind === "user" ? "user-message" : "character-message"}`;
      article.dataset.sequence = String(message.sequence || "");
      if (message.speaker_kind !== "user") {
        const avatar = document.createElement("img");
        avatar.src = message.avatar_url || "";
        avatar.alt = "";
        article.append(avatar);
      }
      const body = document.createElement("div");
      const speaker = document.createElement("strong");
      speaker.className = "speaker-name";
      speaker.textContent = message.display_name || (message.speaker_kind === "user" ? "你" : message.speaker_id);
      const content = document.createElement("p");
      content.textContent = message.content;
      body.append(speaker, content);
      article.append(body);
      transcript.append(article);
    });
    scrollToLatestMessage(transcript);
  }

  function scrollToLatestMessage(transcript) {
    transcript.scrollTop = transcript.scrollHeight;
    const positionLatest = () => {
      transcript.lastElementChild?.scrollIntoView({ block: "end" });
      transcript.scrollTop = transcript.scrollHeight;
    };
    requestAnimationFrame(positionLatest);
    setTimeout(positionLatest, 0);
  }

  function renderPendingState() {
    const pending = Boolean(state.current && state.pendingSpaces.has(state.current.id));
    el("send-button").disabled = !state.current || pending;
    el("message-input").disabled = pending;
    el("clear-button").disabled = !state.current || pending;
    el("discussion-status").hidden = !pending;
    el("discussion-status").querySelector("b").textContent = state.current?.kind === "group"
      ? "SOS 团正在讨论" : `${state.current?.display_name || "角色"}正在回复`;
  }

  function setPending(conversationId, requestId) {
    state.pendingSpaces.set(conversationId, requestId);
    if (state.current?.id === conversationId) renderPendingState();
  }

  function clearPending(conversationId, requestId) {
    if (state.pendingSpaces.get(conversationId) !== requestId) return;
    state.pendingSpaces.delete(conversationId);
    if (state.current?.id === conversationId) renderPendingState();
  }

  function showNotice(text, kind = "info") {
    el("notice").textContent = text;
    el("notice").className = `system-notice ${kind}`;
    el("notice").hidden = false;
  }
  function hideNotice() { el("notice").hidden = true; el("notice").textContent = ""; }

  el("login-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    el("login-error").textContent = "";
    try {
      await api("/api/v1/auth/login", { method: "POST", body: JSON.stringify({
        email: el("email").value, password: el("password").value,
        captcha_id: state.captchaId, captcha_answer: el("captcha-answer").value,
      }) });
      await showChat();
    } catch (error) {
      el("login-error").textContent = error.message;
      await refreshCaptcha();
    }
  });
  el("captcha-refresh").addEventListener("click", refreshCaptcha);

  el("composer").addEventListener("submit", async (event) => {
    event.preventDefault();
    const space = state.current;
    const content = el("message-input").value.trim();
    if (!space || !content) return;
    const requestId = crypto.randomUUID();
    const sessionVersion = state.sessionVersion;
    hideNotice();
    el("message-input").value = "";
    setPending(space.id, requestId);
    try {
      const data = await api(`/api/v1/conversations/${encodeURIComponent(space.id)}/messages`, {
        method: "POST", body: JSON.stringify({ content, client_request_id: requestId }),
      });
      if (sessionVersion !== state.sessionVersion || state.current?.id !== space.id) return;
      const history = await api(`/api/v1/conversations/${encodeURIComponent(space.id)}/messages`);
      if (sessionVersion !== state.sessionVersion || state.current?.id !== space.id) return;
      renderMessages(history.messages || []);
      if (data.batch.status === "partial") showNotice("讨论被中断，已保留成功回复。", "warning");
      if (data.batch.status === "failed") showNotice("本轮没有生成角色回复，请稍后重试。", "warning");
    } catch (error) {
      if (sessionVersion === state.sessionVersion && state.current?.id === space.id) {
        if (!el("message-input").value) el("message-input").value = content;
        showNotice(error.code === "conversation_busy" ? "这个窗口仍在回复，请稍候。" : error.message, "error");
      }
    } finally {
      clearPending(space.id, requestId);
    }
  });

  el("clear-button").addEventListener("click", async () => {
    const space = state.current;
    if (!space || !window.confirm(`清除“${space.display_name}”今天的聊天记录？`)) return;
    try {
      await api(`/api/v1/conversations/${encodeURIComponent(space.id)}/messages`, { method: "DELETE" });
      if (state.current?.id === space.id) renderMessages([]);
    } catch (error) { showNotice(error.message, "error"); }
  });

  el("logout-button").addEventListener("click", async () => {
    try { await api("/api/v1/auth/logout", { method: "POST" }); } finally { await showLogin(); }
  });

  boot();
})();
