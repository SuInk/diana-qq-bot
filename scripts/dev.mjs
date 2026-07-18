#!/usr/bin/env node
import { spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import net from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const frontendDir = path.join(rootDir, "frontend");
const isWindows = process.platform === "win32";
const npmCommand = isWindows ? "npm.cmd" : "npm";

const backendHost = envOr("BACKEND_HOST", "127.0.0.1");
const backendPort = parsePort("BACKEND_PORT", envOr("BACKEND_PORT", envOr("PORT", "18080")));
const frontendHost = envOr("FRONTEND_HOST", "127.0.0.1");
const frontendPort = parsePort("FRONTEND_PORT", envOr("FRONTEND_PORT", "5173"));
const backendTarget = envOr("VITE_BACKEND_TARGET", `http://${backendHost}:${backendPort}`);
const children = [];
let shuttingDown = false;

main().catch((err) => {
  console.error(`[dev] ${err.message}`);
  shutdown(1);
});

async function main() {
  await needCommand("go", ["version"]);
  await needCommand(npmCommand, ["--version"]);
  await ensurePortFree("BACKEND", backendHost, backendPort, { alsoCheckWildcard: true });
  await ensurePortFree("FRONTEND", frontendHost, frontendPort);

  // 首次运行时自动安装前端依赖；正常重启不会重复安装，启动速度更快。
  if (!existsSync(path.join(frontendDir, "node_modules"))) {
    const installCommand = existsSync(path.join(frontendDir, "package-lock.json")) ? "ci" : "install";
    console.log(`[dev] frontend/node_modules not found, running npm ${installCommand}...`);
    await runForeground(npmCommand, [installCommand], { cwd: frontendDir });
  }

  console.log(`[dev] backend:  http://${backendHost}:${backendPort}`);
  console.log(`[dev] frontend: http://${frontendHost}:${frontendPort}`);
  console.log(`[dev] proxy:    ${backendTarget}`);
  console.log("");

  process.on("SIGINT", () => shutdown(0));
  process.on("SIGTERM", () => shutdown(0));

  startService("backend", "go", ["run", "./cmd/webui"], {
    cwd: rootDir,
    env: {
      ...process.env,
      BACKEND_HOST: backendHost,
      HOST: backendHost,
      PORT: String(backendPort),
      QQBOT_GROUP_TEST_ENABLED: envOr("QQBOT_GROUP_TEST_ENABLED", "true"),
    },
  });

  startService("frontend", npmCommand, ["run", "dev", "--", "--host", frontendHost, "--port", String(frontendPort), "--strictPort"], {
    cwd: frontendDir,
    env: { ...process.env, VITE_BACKEND_TARGET: backendTarget },
  });
}

function envOr(key, fallback) {
  const value = (process.env[key] || "").trim();
  return value || fallback;
}

function parsePort(name, raw) {
  const port = Number.parseInt(String(raw), 10);
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`${name} must be a valid TCP port, got ${raw}`);
  }
  return port;
}

function needCommand(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: "ignore", windowsHide: true });
    child.on("error", () => reject(new Error(`missing command: ${command.replace(/\.cmd$/, "")}`)));
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`command check failed: ${command.replace(/\.cmd$/, "")}`));
      }
    });
  });
}

async function ensurePortFree(name, host, port, options = {}) {
  const targets = [{ host, port }];
  if (options.alsoCheckWildcard && !isWildcardHost(host)) {
    targets.push({ host: "", port });
  }
  for (const target of targets) {
    await ensureBindTargetFree(name, target.host, target.port);
  }
}

function ensureBindTargetFree(name, host, port) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: connectionHost(host), port });
    socket.once("connect", () => {
      socket.destroy();
      reject(portInUseError(name, port));
    });
    socket.once("error", (connectErr) => {
      if (connectErr.code !== "ECONNREFUSED" && connectErr.code !== "EHOSTUNREACH" && connectErr.code !== "ENETUNREACH") {
        reject(new Error(`${name} port ${port} check failed: ${connectErr.message}`));
        return;
      }
      const server = net.createServer();
      server.once("error", (listenErr) => {
        if (listenErr.code === "EADDRINUSE") {
          reject(portInUseError(name, port));
          return;
        }
        reject(new Error(`${name} port ${port} check failed: ${listenErr.message}`));
      });
      server.once("listening", () => {
        server.close(resolve);
      });
      server.listen({ host, port });
    });
  });
}

function isWildcardHost(host) {
  return host === "" || host === "0.0.0.0" || host === "::" || host === "[::]";
}

function connectionHost(host) {
  return isWildcardHost(host) ? "127.0.0.1" : host;
}

function portInUseError(name, port) {
  return new Error(`${name} port ${port} is already in use. Set ${name === "BACKEND" ? "BACKEND_PORT" : "FRONTEND_PORT"} to another port, or stop the existing process.`);
}

function runForeground(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: "inherit", windowsHide: false });
    child.on("error", (err) => reject(err));
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`${command} ${args.join(" ")} exited with code ${code}`));
      }
    });
  });
}

function startService(label, command, args, options) {
  // macOS/Linux 使用独立进程组，退出时能一起清理 go run 派生出的后端进程。
  const child = spawn(command, args, {
    ...options,
    stdio: "inherit",
    detached: !isWindows,
    windowsHide: false,
  });
  children.push(child);

  child.on("error", (err) => {
    if (!shuttingDown) {
      console.error(`[dev] ${label} failed to start: ${err.message}`);
      shutdown(1);
    }
  });
  child.on("exit", (code, signal) => {
    if (!shuttingDown) {
      const reason = signal ? `signal ${signal}` : `code ${code}`;
      console.error(`[dev] ${label} exited with ${reason}`);
      shutdown(code || 1);
    }
  });
}

function shutdown(code) {
  if (shuttingDown) {
    return;
  }
  shuttingDown = true;
  for (const child of children) {
    killTree(child, false);
  }
  // go run 会再派生真正的后端二进制，给它一个响应 SIGTERM 的窗口，再兜底强制清理。
  setTimeout(() => {
    for (const child of children) {
      killTree(child, true);
    }
    process.exit(code);
  }, 1200);
}

function killTree(child, force) {
  if (!child.pid) {
    return;
  }
  if (isWindows) {
    const args = ["/pid", String(child.pid), "/T"];
    if (force) {
      args.push("/F");
    }
    spawnSync("taskkill", args, { stdio: "ignore" });
    return;
  }
  try {
    process.kill(-child.pid, force ? "SIGKILL" : "SIGTERM");
  } catch {
    try {
      child.kill(force ? "SIGKILL" : "SIGTERM");
    } catch {
      // 进程可能已经退出，清理失败可以忽略。
    }
  }
}
