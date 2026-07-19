import { chromium } from "@playwright/test";

const baseURL = process.env.WEBUI_URL || "http://127.0.0.1:5173";
const screenshotPath = process.env.WEBUI_SCREENSHOT || "/tmp/diana-qqbot-dev-page.png";

function fail(message) {
  throw new Error(message);
}

const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });

try {
  await page.goto(new URL("/qqbot", baseURL).toString(), { waitUntil: "networkidle" });

  const versionVisible = await page.getByText("v0.1.0").first().isVisible().catch(() => false);
  const groupTestVisible = await page.getByText("QQ群收发测试").isVisible().catch(() => false);
  const sendButtonVisible = await page.getByRole("button", { name: /发送到 QQ 群/ }).isVisible().catch(() => false);
  const restfulAPIVisible = await page.getByText("RESTful API").isVisible().catch(() => false);

  if (!versionVisible) {
    fail("expected sidebar version v0.1.0 to be visible");
  }
  if (!groupTestVisible) {
    fail("expected dev-only QQ group test panel to be visible");
  }
  if (!sendButtonVisible) {
    fail("expected QQ group test send button to be visible");
  }
  if (restfulAPIVisible) {
    fail("RESTful API entry should not be visible");
  }

  await page.screenshot({ path: screenshotPath, fullPage: false });
  console.log(
    JSON.stringify(
      {
        ok: true,
        base_url: baseURL,
        route: "/qqbot",
        version_visible: versionVisible,
        group_test_visible: groupTestVisible,
        send_button_visible: sendButtonVisible,
        restful_api_visible: restfulAPIVisible,
        screenshot: screenshotPath
      },
      null,
      2
    )
  );
} finally {
  await browser.close();
}
