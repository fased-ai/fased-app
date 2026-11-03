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
