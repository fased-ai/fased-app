(async () => {
  let phase = "Local owner entry";
  const pause = (ms) => new Promise((done) => setTimeout(done, ms));
  const wait = async (check) => {
    for (let i = 0; i < 100; i++) {
      const value = check();
      if (value) return value;
      await pause(100);
    }
    throw new Error("Native UI did not reach the expected state");
  };
  try {
    await wait(
      () =>
        document.querySelector("main#main-content") &&
        document.querySelector('button[aria-label="Sign out"]'),
    );
    if (
      document.getElementById("owner-code") ||
      document.getElementById("owner-password")
    )
      throw new Error(
        "Local native entry must not require a code or app password",
      );
    phase = "authenticated setup";
    const token = sessionStorage.getItem("fased.core.session.v1");
    if (!token) throw new Error("Native bearer session missing");
    const headers = { Authorization: `Bearer ${token}` };
    const auth = await (await fetch("/api/v1/auth/state", { headers })).json();
    const record = await (await fetch("/api/v1/setup", { headers })).json();
    const updated = await fetch("/api/v1/setup", {
      method: "PUT",
      headers: {
        ...headers,
        "Content-Type": "application/json",
        "X-CSRF-Token": auth.csrfToken,
      },
      body: JSON.stringify({
        revision: record.revision,
        settings: { ...record.settings, host: "vps" },
      }),
    });
    if (updated.status !== 200)
      throw new Error(`Native setup rejected: ${updated.status}`);
    phase = "provider and profile transport";
    const provider = await fetch("/api/v1/ai/connection", { headers });
    if (provider.status !== 200)
      throw new Error(`Provider read: ${provider.status}`);
    const agents = await fetch("/api/v1/agents?profile=news-researcher", {
      headers,
    });
    const view = await agents.json();
    if (agents.status !== 200 || view.profileId !== "news-researcher")
      throw new Error("Native profile query was rejected or dropped");
    const conversation = await fetch("/api/v1/agents", {
      method: "POST",
      headers: {
        ...headers,
        "Content-Type": "application/json",
        "X-CSRF-Token": auth.csrfToken,
      },
      body: JSON.stringify({
        action: "session-create",
        revision: view.revision,
        profileId: view.profileId,
        name: "Native smoke",
      }),
    });
    if (conversation.status !== 200)
      throw new Error(`Conversation write: ${conversation.status}`);
    phase = "Agent V2 threads, memories and MCP";
    const jsonHeaders = {
      ...headers,
      "Content-Type": "application/json",
      "X-CSRF-Token": auth.csrfToken,
    };
    const toolsResp = await fetch("/api/v1/agents/v2/tools", { headers });
    if (toolsResp.status !== 200)
      throw new Error(`Agent V2 tools read: ${toolsResp.status}`);
    const usageResp = await fetch("/api/v1/agents/v2/usage?days=7", {
      headers,
    });
    if (usageResp.status !== 200)
      throw new Error(`Agent V2 usage read: ${usageResp.status}`);
    const manifestResp = await fetch("/api/v1/agents/v2/model-manifest", {
      headers,
    });
    if (manifestResp.status !== 200)
      throw new Error(`Agent V2 manifest read: ${manifestResp.status}`);
    const createdThreadResp = await fetch("/api/v1/agents/v2/threads", {
      method: "POST",
      headers: jsonHeaders,
      body: JSON.stringify({
        profileId: view.profileId,
        title: "Native V2 smoke",
      }),
    });
    const createdThread = await createdThreadResp.json();
    if (createdThreadResp.status !== 201 || !createdThread?.thread?.id)
      throw new Error(`Agent V2 thread create: ${createdThreadResp.status}`);
    const threadId = createdThread.thread.id;
    const renamedThreadResp = await fetch(
      `/api/v1/agents/v2/threads/${encodeURIComponent(threadId)}`,
      {
        method: "PATCH",
        headers: jsonHeaders,
        body: JSON.stringify({ title: "Native V2 renamed" }),
      },
    );
    const renamedThread = await renamedThreadResp.json();
    if (
      renamedThreadResp.status !== 200 ||
      renamedThread?.thread?.title !== "Native V2 renamed"
    )
      throw new Error(`Agent V2 thread rename: ${renamedThreadResp.status}`);
    const threadDetailResp = await fetch(
      `/api/v1/agents/v2/threads/${encodeURIComponent(threadId)}`,
      { headers },
    );
    if (threadDetailResp.status !== 200)
      throw new Error(`Agent V2 thread detail: ${threadDetailResp.status}`);
    const createdMemoryResp = await fetch("/api/v1/agents/v2/memories", {
      method: "POST",
      headers: jsonHeaders,
      body: JSON.stringify({
        profileId: view.profileId,
        scope: "chat",
        title: "Native rule",
        content: "Keep responses concise.",
        pinned: true,
      }),
    });
    const createdMemory = await createdMemoryResp.json();
    if (createdMemoryResp.status !== 201 || !createdMemory?.memory?.id)
      throw new Error(`Agent V2 memory create: ${createdMemoryResp.status}`);
    const memId = createdMemory.memory.id;
    const listMemResp = await fetch(
      `/api/v1/agents/v2/memories?profileId=${encodeURIComponent(view.profileId)}&excludeDrafts=1&limit=10`,
      { headers },
    );
    if (listMemResp.status !== 200)
      throw new Error(`Agent V2 memory list: ${listMemResp.status}`);
    const patchMemResp = await fetch(
      `/api/v1/agents/v2/memories/${encodeURIComponent(memId)}`,
      {
        method: "PATCH",
        headers: jsonHeaders,
        body: JSON.stringify({ content: "Keep responses very concise." }),
      },
    );
    if (patchMemResp.status !== 200)
      throw new Error(`Agent V2 memory patch: ${patchMemResp.status}`);
    const delMemResp = await fetch(
      `/api/v1/agents/v2/memories/${encodeURIComponent(memId)}`,
      {
        method: "DELETE",
        headers: jsonHeaders,
      },
    );
    if (delMemResp.status !== 200)
      throw new Error(`Agent V2 memory delete: ${delMemResp.status}`);
    const delThreadResp = await fetch(
      `/api/v1/agents/v2/threads/${encodeURIComponent(threadId)}`,
      {
        method: "DELETE",
        headers: jsonHeaders,
      },
    );
    if (delThreadResp.status !== 200)
      throw new Error(`Agent V2 thread delete: ${delThreadResp.status}`);
    const mcpInfoResp = await fetch("/api/v1/mcp", { headers });
    if (mcpInfoResp.status !== 200)
      throw new Error(`MCP info read: ${mcpInfoResp.status}`);
    const mcpPingResp = await fetch("/api/v1/mcp", {
      method: "POST",
      headers: jsonHeaders,
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "ping" }),
    });
    const mcpPing = await mcpPingResp.json();
    if (mcpPingResp.status !== 200 || mcpPing?.jsonrpc !== "2.0")
      throw new Error(`MCP ping: ${mcpPingResp.status}`);
    window.runtime.EventsEmit(
      "fased-native-smoke",
      JSON.stringify({
        ok: true,
        checks: [
          "real WebKit window",
          "shared production UI",
          "Local OS owner entry without app password",
          "bearer and CSRF",
          "private IPC setup",
          "provider read",
          "profile query and conversation write",
          "Agent V2 threads, memories and MCP",
        ],
      }),
    );
  } catch (error) {
    let auth;
    try {
      const response = await fetch("/api/v1/auth/state");
      auth = {
        status: response.status,
        contentType: response.headers.get("content-type"),
        body: (await response.text()).slice(0, 300),
      };
    } catch (failure) {
      auth = { error: failure.message };
    }
    window.runtime.EventsEmit(
      "fased-native-smoke",
      JSON.stringify({
        ok: false,
        phase,
        error: error.message,
        href: location.href,
        origin: location.origin,
        screen: document.body.innerText.slice(0, 300),
        uiError: document.querySelector(".core-error")?.textContent,
        inputs: [...document.querySelectorAll(".core-auth input")].map(
          (field) => ({
            id: field.id,
            length: field.value.length,
            valid: field.checkValidity(),
          }),
        ),
        auth,
      }),
    );
  }
})();
