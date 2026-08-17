// Universal watchdog: polls user-defined check scripts and notifies the
// opencode session (prompt + TUI toast) when a check's condition flips to true.
//
// Rules live in .opencode/watchdog.json. The plugin knows NOTHING about what
// a rule means — semantics live entirely in the check scripts. Contract:
//
//   check script exit 0  → condition true  → fire once (edge-triggered),
//                          script stdout is substituted into the prompt as {{output}}
//   check script exit 1  → condition false → rearm the rule
//   any other exit code  → check error     → logged, rule rearmed
//
// The first tick after startup only establishes the baseline (never fires),
// so a long-true condition doesn't alarm on every opencode restart.
//
// Delivery on fire: prompt into session(s) + TUI toast + desktop notification
// (notify-send, best effort). Default routing: the most recently updated
// session. Rule option "broadcast": true prompts ALL sessions instead — safe
// when agents coordinate claims out-of-band (e.g. the git claim protocol).
// Delivery failures are logged, never swallowed. Rule option "remind_s":
// re-deliver every N seconds while the condition stays true, so a missed
// notification is not lost.

const DEFAULT_TIMEOUT_S = 60;

async function log(client, level, message) {
  try {
    await client.app.log({
      body: { service: "watchdog", level, message },
    });
  } catch {
    // Logging must never break the poller.
  }
}

async function runCheck(directory, checkPath, timeoutS, extraEnv) {
  const proc = Bun.spawn(["bash", checkPath], {
    cwd: directory,
    stdout: "pipe",
    stderr: "pipe",
    env: { ...process.env, REPO_ROOT: directory, ...(extraEnv ?? {}) },
  });
  const killer = setTimeout(() => proc.kill(), timeoutS * 1000);
  const code = await proc.exited;
  clearTimeout(killer);
  const stdout = await new Response(proc.stdout).text();
  const stderr = await new Response(proc.stderr).text();
  return { code, stdout: stdout.trim(), stderr: stderr.trim() };
}

async function desktopNotify(client, text) {
  try {
    const proc = Bun.spawn(["notify-send", "--app-name=Orenda", "Orenda watchdog", text.slice(0, 300)], {
      stdout: "ignore",
      stderr: "pipe",
    });
    const code = await proc.exited;
    if (code !== 0) {
      const err = await new Response(proc.stderr).text();
      await log(client, "warn", `watchdog: notify-send exited ${code}: ${err.trim()}`);
    }
  } catch (err) {
    // notify-send absent (headless server, macOS) — prompt path still applies.
    if (!String(err).includes("ENOENT")) {
      await log(client, "warn", `watchdog: desktop notify failed: ${err}`);
    }
  }
}

async function deliver(client, rule, output) {
  const template = rule.prompt ?? `Watchdog rule "${rule.name}" fired.`;
  const text = template.replace("{{output}}", output || "(no details)");

  await desktopNotify(client, text);

  try {
    await client.tui.showToast({
      body: { title: "Watchdog", message: rule.name, variant: "warning" },
    });
  } catch (err) {
    await log(client, "warn", `watchdog[${rule.name}]: toast failed: ${err}`);
  }

  try {
    const res = await client.session.list();
    const sessions = Array.isArray(res.data) ? res.data : [];
    if (sessions.length === 0) {
      await log(client, "warn", `watchdog[${rule.name}]: fired but no session to prompt`);
      return;
    }
    sessions.sort((a, b) => (b.time?.updated ?? 0) - (a.time?.updated ?? 0));
    const targets = rule.broadcast ? sessions : [sessions[0]];
    for (const target of targets) {
      try {
        await client.session.prompt({
          path: { id: target.id },
          body: { parts: [{ type: "text", text }] },
        });
        await log(client, "info", `watchdog[${rule.name}]: fired, prompt delivered to session ${target.id}`);
      } catch (err) {
        await log(client, "error", `watchdog[${rule.name}]: prompt to session ${target.id} failed: ${err}`);
      }
    }
  } catch (err) {
    await log(client, "error", `watchdog[${rule.name}]: prompt failed: ${err}`);
  }
}

function startRule(client, directory, rule) {
  const checkPath = rule.check.startsWith("/") ? rule.check : `${directory}/${rule.check}`;
  const timeoutS = rule.timeout_s ?? DEFAULT_TIMEOUT_S;
  const remindMs = rule.remind_s ? rule.remind_s * 1000 : 0;
  let fired = false;
  let lastFiredAt = 0;
  let firstTick = true;

  const tick = async () => {
    let result;
    try {
      result = await runCheck(directory, checkPath, timeoutS, rule.env);
    } catch (err) {
      await log(client, "error", `watchdog[${rule.name}]: spawn failed: ${err}`);
      return;
    }

    const baseline = firstTick;
    firstTick = false;

    if (result.code === 0) {
      const remindDue = remindMs > 0 && fired && Date.now() - lastFiredAt >= remindMs;
      if (!fired || remindDue) {
        fired = true;
        lastFiredAt = Date.now();
        if (!baseline) await deliver(client, rule, result.stdout);
      }
    } else if (result.code === 1) {
      fired = false;
      lastFiredAt = 0;
    } else {
      fired = false;
      lastFiredAt = 0;
      await log(
        client,
        "warn",
        `watchdog[${rule.name}]: check exited ${result.code}: ${result.stderr || "(no stderr)"}`
      );
    }
  };

  void tick();
  const timer = setInterval(() => void tick(), rule.interval_s * 1000);
  timer.unref?.();
}

export const Watchdog = async ({ client, directory }) => {
  const configPath = `${directory}/.opencode/watchdog.json`;
  const file = Bun.file(configPath);
  if (!(await file.exists())) return {};

  let config;
  try {
    config = await file.json();
  } catch (err) {
    await log(client, "error", `watchdog: cannot parse ${configPath}: ${err}`);
    return {};
  }

  const rules = Array.isArray(config.rules) ? config.rules : [];
  for (const rule of rules) {
    if (!rule.name || !rule.check || !rule.interval_s) {
      await log(client, "warn", `watchdog: skipping malformed rule: ${JSON.stringify(rule)}`);
      continue;
    }
    startRule(client, directory, rule);
  }
  await log(client, "info", `watchdog: ${rules.length} rule(s) armed`);
  return {};
};
