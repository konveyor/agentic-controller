import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

const baseURL = process.env.DEMO_URL ?? "http://127.0.0.1:18081";
const output = path.resolve(process.env.DEMO_OUTPUT ?? "artifacts/inspect");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
const observations = { pageErrors: [], consoleErrors: [], failedRequests: [], badResponses: [] };
const safeURL = (raw) => {
  const url = new URL(raw);
  return url.origin + url.pathname;
};
page.on("pageerror", (error) => observations.pageErrors.push(error.stack ?? error.message));
page.on("console", (message) => {
  if (message.type() === "error") observations.consoleErrors.push(message.text());
});
page.on("requestfailed", (request) => observations.failedRequests.push({
  url: safeURL(request.url()), error: request.failure()?.errorText,
}));
page.on("response", (response) => {
  if (response.status() >= 400) observations.badResponses.push({ url: safeURL(response.url()), status: response.status() });
});
try {
  await page.goto(baseURL + "/applications", { waitUntil: "networkidle", timeout: 45000 });
  observations.url = safeURL(page.url());
  observations.title = await page.title();
  observations.bodyText = await page.locator("body").innerText();
  await page.screenshot({ path: path.join(output, "applications.png"), fullPage: true });
} catch (error) {
  observations.failure = error.message;
  await page.screenshot({ path: path.join(output, "failure.png"), timeout: 5000 }).catch(() => {});
} finally {
  await writeFile(path.join(output, "observations.json"), JSON.stringify(observations, null, 2) + "\n");
  console.log(JSON.stringify(observations, null, 2));
  await browser.close();
}
if (observations.failure || observations.pageErrors.length) process.exitCode = 1;
