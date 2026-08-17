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

async function deliver(client, rule, output) {
  const template = rule.prompt ?? `Watchdog rule "${rule.name}" fired.`;
  const text = template.replace("{{output}}", output || "(no details)");

  try {
    await client.tui.showToast({
      body: { title: "Watchdog", message: rule.name, variant: "info" },
    });
  } catch {
    // TUI may be absent (headless/server mode) — prompt path still applies.
  }

  try {
    const res = await client.session.list();
    const sessions = Array.isArray(res.data) ? res.data : [];
    if (sessions.length === 0) {
      await log(client, "warn", `watchdog[${rule.name}]: fired but no session to prompt`);
      return;
    }
    sessions.sort((a, b) => (b.time?.updated ?? 0) - (a.time?.updated ?? 0));
    await client.session.prompt({
      path: { id: sessions[0].id },
      body: { parts: [{ type: "text", text }] },
    });
    await log(client, "info", `watchdog[${rule.name}]: fired, prompt delivered`);
  } catch (err) {
    await log(client, "error", `watchdog[${rule.name}]: prompt failed: ${err}`);
  }
}

function startRule(client, directory, rule) {
  const checkPath = rule.check.startsWith("/") ? rule.check : `${directory}/${rule.check}`;
  const timeoutS = rule.timeout_s ?? DEFAULT_TIMEOUT_S;
  let fired = false;
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
      if (!fired) {
        fired = true;
        if (!baseline) await deliver(client, rule, result.stdout);
      }
    } else if (result.code === 1) {
      fired = false;
    } else {
      fired = false;
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
