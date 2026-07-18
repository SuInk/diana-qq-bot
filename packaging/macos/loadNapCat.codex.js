const fs = require("fs");
const os = require("os");
const path = require("path");
const { pathToFileURL } = require("url");

const qqAppDir = __dirname;
const napcatWorkDir = path.join(os.homedir(), "Library", "Application Support", "QQ", "NapCat");
const napcatMain = path.join(napcatWorkDir, "runtime", "napcat.mjs");
const originalMain = path.join(qqAppDir, "application.asar", "app_launcher", "index.js");
const logFile = path.join(napcatWorkDir, "logs", "loader.log");

process.env.NAPCAT_QQ_PACKAGE_INFO_PATH = path.join(qqAppDir, "package.json");
process.env.NAPCAT_WRAPPER_PATH = path.join(qqAppDir, "wrapper.node");
process.env.NAPCAT_WORKDIR = napcatWorkDir;
process.env.NAPCAT_WEBUI_PREFERRED_PORT = process.env.NAPCAT_WEBUI_PREFERRED_PORT || "6100";

function log(message) {
  try {
    fs.mkdirSync(path.dirname(logFile), { recursive: true });
    fs.appendFileSync(logFile, `[${new Date().toISOString()}] ${message}\n`);
  } catch {}
}

function formatError(error) {
  if (!error) return "<empty error>";
  return error.stack || error.message || String(error);
}

process.on("uncaughtException", (error) => log(`uncaughtException: ${formatError(error)}`));
process.on("unhandledRejection", (error) => log(`unhandledRejection: ${formatError(error)}`));

const useNapCat = process.argv.some((arg) => arg === "--no-sandbox" || arg.includes("no-sandbox"));
log(`loader start; useNapCat=${useNapCat}; main=${napcatMain}`);

if (!useNapCat) {
  require(originalMain);
} else if (!fs.existsSync(napcatMain)) {
  log(`NapCat runtime is missing: ${napcatMain}`);
  require(originalMain);
} else {
  import(pathToFileURL(napcatMain).href)
    .then(() => log("NapCat import resolved"))
    .catch((error) => {
      log(`NapCat import failed: ${formatError(error)}`);
      setTimeout(() => process.exit(1), 3000);
    });
}
