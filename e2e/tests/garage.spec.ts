import { chromium, expect, test, type Page, type Request } from "@playwright/test";
import * as zlib from "node:zlib";

// One realistic walk through the app, using the inventory from the
// original request. Tests run in order and build on each other.
test.describe.configure({ mode: "serial" });

type ItemSpec = {
  brand?: string;
  name: string;
  volume?: boolean;
  qty: string;
  qtyUnit?: "pcs" | "ml" | "l";
  min?: string;
  vehicles: string[];
  category?: string;
  skus?: { code: string; kind?: string; label?: string }[];
};

const exact = (s: string) => new RegExp(`^\\s*${s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);

async function addVehicle(page: Page, name: string, odometer: string) {
  await page.goto("/vehicles/new");
  await page.getByLabel("Name").fill(name);
  await page.getByLabel("Odometer (km)").fill(odometer);
  await page.getByRole("button", { name: "Save vehicle" }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
}

async function addItem(page: Page, it: ItemSpec) {
  await page.goto("/items/new");
  await page.getByLabel("Name", { exact: true }).fill(it.name);
  if (it.brand) await page.getByLabel("Brand").fill(it.brand);
  if (it.volume) await page.locator("label", { hasText: "Volume (ml / L)" }).click();
  await page.getByLabel("On stock").fill(it.qty);
  if (it.qtyUnit) await page.locator('select[name="quantity_unit"]').selectOption(it.qtyUnit);
  if (it.min) await page.getByLabel("Keep at least").fill(it.min);
  if (it.category) await page.getByLabel("Category").fill(it.category);
  for (const v of it.vehicles) {
    await page.locator("label.chip-toggle").filter({ hasText: exact(v) }).click();
  }
  for (const [i, sku] of (it.skus ?? []).entries()) {
    if (i > 0) await page.getByRole("button", { name: "Add SKU" }).click();
    await page.locator('input[name="sku_code"]').nth(i).fill(sku.code);
    if (sku.kind) await page.locator('select[name="sku_kind"]').nth(i).selectOption(sku.kind);
    if (sku.label) await page.locator('input[name="sku_label"]').nth(i).fill(sku.label);
  }
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".flash-success")).toContainText("Added");
}

function row(page: Page, name: string) {
  return page.locator(".item-row").filter({ hasText: name });
}

test("set up vehicles and the example inventory", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Welcome to Pitbox")).toBeVisible();

  await addVehicle(page, "KTM 390", "11200");
  await addVehicle(page, "KTM 1290", "24500");

  const ktm390: ItemSpec[] = [
    { brand: "OEM", name: "Rear Brake Pads", qty: "1", vehicles: ["KTM 390"], skus: [{ code: "90113090000" }] },
    { brand: "OEM", name: "Fuel Filter", qty: "1", vehicles: ["KTM 390"] },
    { brand: "OEM", name: "Air Filter", qty: "1", vehicles: ["KTM 390"] },
    { brand: "OEM", name: "Oil Filter", qty: "1", min: "1", vehicles: ["KTM 390"], skus: [{ code: "90138015000" }] },
    { brand: "OEM", name: "Oil Screen 1 O-Ring", qty: "2", vehicles: ["KTM 390"] },
    { brand: "OEM", name: "Oil Screen 2 O-Ring", qty: "3", vehicles: ["KTM 390"] },
    {
      brand: "Silkolene", name: "15W50 PRO 4 XP", volume: true, qty: "525", qtyUnit: "ml", vehicles: ["KTM 390"], category: "Oil & fluids",
      skus: [
        { code: "SK-80085", kind: "manufacturer", label: "1 L" },
        { code: "SK-80086", kind: "manufacturer", label: "4 L" },
        { code: "SK-80087", kind: "manufacturer", label: "20 L" },
        { code: "SK-80088", kind: "manufacturer", label: "60 L" },
      ],
    },
  ];
  const ktm1290: ItemSpec[] = [
    { brand: "OEM", name: "Rear Brake Pads (1290)", qty: "1", vehicles: ["KTM 1290"] },
    { brand: "Hiflofiltro", name: "Oil Filter", qty: "1", vehicles: ["KTM 1290"], skus: [{ code: "HF-650", kind: "manufacturer" }] },
    { brand: "OEM", name: "Oil Screen 1+2 O-Ring", qty: "6", vehicles: ["KTM 1290"] },
    { brand: "Motorex", name: "10W50 Power Synt 4T", volume: true, qty: "1.5", qtyUnit: "l", vehicles: ["KTM 1290"] },
  ];
  const both: ItemSpec[] = [
    { brand: "OEM", name: "Oil Filter O-Ring", qty: "6", vehicles: ["KTM 390", "KTM 1290"] },
    { brand: "Motorex", name: "M3.0 Ready To Use", volume: true, qty: "640", qtyUnit: "ml", vehicles: ["KTM 390", "KTM 1290"] },
  ];
  for (const it of [...ktm390, ...ktm1290, ...both]) await addItem(page, it);

  await page.goto("/");
  await expect(page.locator(".tile").filter({ hasText: "Items" })).toContainText("13");
});

test("filter stock by vehicle and copy it as text", async ({ page, context }, testInfo) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/stock");
  await expect(page.locator(".section")).toHaveCount(3);

  await page.locator("#filters label.chip-toggle").filter({ hasText: exact("KTM 390") }).click();
  await expect(page).toHaveURL(/[?&]v=\d+/);
  await expect(page.locator(".section")).toHaveCount(2);
  await expect(page.locator(".section").first()).toContainText("Silkolene");
  await expect(page.locator(".section-shared")).toContainText("M3.0 Ready To Use");
  await expect(page.locator("#results")).not.toContainText("Hiflofiltro");
  await page.screenshot({ path: testInfo.outputPath("stock-ktm390.png"), fullPage: true });

  // A link to the text, which JS copies instead of opening.
  const copy = page.getByRole("link", { name: "Copy as text" });
  await copy.click();
  await expect(copy).toHaveAttribute("data-copied", /KTM 390\n-{12}\n/);
  await expect(page.locator(".toast")).toContainText("Copied to clipboard");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("525 ml - Silkolene 15W50 PRO 4 XP");
  const text = await copy.getAttribute("data-copied");
  expect(text).toContain("525 ml - Silkolene 15W50 PRO 4 XP");
  expect(text).toContain("KTM 390 + KTM 1290\n------------------\n640 ml - Motorex M3.0 Ready To Use\n6x - OEM Oil Filter O-Ring");
  expect(text).not.toContain("Hiflofiltro");

  // Ticking a second vehicle offers "fits all" without a reload.
  await expect(page.getByText("Fits all")).toHaveCount(0);
  await expect(page.locator("#filter-all")).not.toHaveClass(/active/);
  await page.locator("#filters label.chip-toggle").filter({ hasText: exact("KTM 1290") }).click();
  await page.getByText("Fits all").click();
  await expect(page).toHaveURL(/match=all/);
  await expect(page.locator(".item-row")).toHaveCount(2);
  await expect(page.locator(".section-shared")).toContainText("Oil Filter O-Ring");

  // Search by SKU, with or without the dash.
  await page.goto("/stock");
  await page.locator("#q").fill("hf650");
  await expect(page.locator(".item-row")).toHaveCount(1);
  await expect(page.locator(".item-row")).toContainText("Hiflofiltro");

  // Only three part numbers show in a row; "+1 more" reveals the rest in place.
  const silk = page.locator(".item-row").filter({ hasText: "Silkolene" });
  await page.goto("/stock");
  await expect(silk.locator(".sku:visible")).toHaveCount(3);
  await silk.getByRole("link", { name: "Show 1 more part number" }).click();
  await expect(page).toHaveURL(/\/stock$/);
  await expect(silk.locator(".sku:visible")).toHaveCount(4);
  await expect(silk.getByRole("link", { name: "SK-80088" })).toBeFocused();

  // The category sits next to the vehicle chips and links back to the filtered stock.
  await silk.locator(".item-name").click();
  await page.locator(".item-category").click();
  await expect(page).toHaveURL(/cat=Oil(\+|%20)%26(\+|%20)fluids/);
  await expect(page.locator(".item-row")).toHaveCount(1);
});

test("quick adjust and record a purchase", async ({ page }) => {
  await page.goto("/stock");
  const oil = row(page, "15W50 PRO 4 XP");
  await expect(oil.locator(".qty")).toHaveText("525 ml");
  await oil.getByRole("button", { name: /^Add 100 ml of/ }).click();
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("625 ml");
  await row(page, "15W50 PRO 4 XP").getByRole("button", { name: /^Use 100 ml of/ }).click();
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("525 ml");

  await row(page, "15W50 PRO 4 XP").getByRole("link").first().click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Silkolene 15W50 PRO 4 XP");
  const bought = page.locator("#panel-purchase");
  await bought.getByLabel("How much did you buy?").fill("1");
  await bought.locator('select[name="amount_unit"]').selectOption("l");
  await bought.getByLabel("Total price").fill("18,50");
  await bought.getByLabel("Shop").fill("Moto shop");
  await bought.getByRole("button", { name: "Add to stock" }).click();
  await expect(page.locator("#item-qty")).toHaveText("1\u202f525 ml");
  await expect(page.locator("section").filter({ hasText: "History" }).locator("table")).toContainText("Moto shop");
});

test("fix a wrong price in the history", async ({ page }) => {
  await page.goto("/stock");
  await row(page, "15W50 PRO 4 XP").getByRole("link").first().click();
  const history = page.locator("section").filter({ hasText: "History" });
  await history.locator("tr").filter({ hasText: "Moto shop" }).getByRole("link", { name: /Edit this Purchase/ }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Edit purchase");
  await expect(page.getByLabel("Total price")).toHaveValue("18.50");
  await page.getByLabel("Total price").fill("16,50");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.locator(".flash-success")).toContainText("Updated the purchase");
  await expect(page.locator("#item-qty")).toHaveText("1\u202f525 ml");
  await expect(page.locator('dt:text-is("Last price") + dd')).toHaveText("16.50 EUR / L");
});

test("plan an oil service, shop for what is missing and log it", async ({ page }, testInfo) => {
  page.on("dialog", (d) => d.accept());
  await page.goto("/vehicles");
  await page.locator("a.vehicle-card").filter({ has: page.locator(".name", { hasText: exact("KTM 390") }) }).click();
  await page.getByRole("link", { name: "New kit" }).click();

  await page.getByLabel("Name").fill("Oil service");
  await page.getByLabel("Kilometres").fill("7500");
  await page.getByLabel("Months").fill("12");
  const items = page.locator('select[name="line_item"]');
  const qtys = page.locator('input[name="line_qty"]');
  await items.nth(0).selectOption({ label: "OEM Oil Filter" });
  await qtys.nth(0).fill("1");
  await items.nth(1).selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await qtys.nth(1).fill("1.7 L");
  await items.nth(2).selectOption({ label: "OEM Oil Screen 1 O-Ring" });
  await qtys.nth(2).fill("1");
  await page.getByRole("button", { name: "Save kit" }).click();

  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil service");
  await expect(page.locator(".page-head")).toContainText("1 part short");
  await expect(page.locator(".page-head")).toContainText("Not done yet");
  await page.screenshot({ path: testInfo.outputPath("kit-short.png"), fullPage: true });

  await page.getByRole("button", { name: "Add missing to shopping list" }).click();
  await expect(page.locator(".flash-success")).toContainText("missing");
  await page.goto("/shopping");
  const entry = page.locator(".list li").filter({ hasText: "Silkolene" }).first();
  await expect(entry).toContainText("175 ml");
  await entry.getByRole("link", { name: "Bought" }).click();
  await page.getByLabel("Amount").fill("4");
  await page.locator('select[name="amount_unit"]').selectOption("l");
  await page.getByRole("button", { name: "Add to stock" }).click();
  await expect(page.locator(".flash-success")).toContainText("Added 4 L");

  await page.goto("/services");
  await page.getByRole("link", { name: /Oil service/ }).first().click();
  await expect(page.locator(".page-head")).toContainText("Parts ready");
  await page.getByRole("link", { name: "Do this service" }).click();
  await page.getByLabel("Odometer (km)").fill("12000");
  await page.getByRole("button", { name: /Save & take parts/ }).click();

  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil service");
  await expect(page.locator("main")).toContainText("1.7 L");
  await expect(page.locator("main")).toContainText("12\u202f000 km");

  await page.goto("/stock");
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("3\u202f825 ml");
  await expect(row(page, "OEM Oil Filter").filter({ hasNotText: "O-Ring" }).locator(".qty")).toHaveText("0x");

  await page.goto("/");
  await expect(page.locator("main")).toContainText("in 7\u202f500 km");
  await page.screenshot({ path: testInfo.outputPath("dashboard.png"), fullPage: true });
});

test("buy something new from the shopping list", async ({ page }) => {
  await page.goto("/shopping");
  await page.getByLabel("Something new").fill("Chain lube");
  await page.getByLabel("Quantity").fill("2");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await page.locator(".list li").filter({ hasText: "Chain lube" }).getByRole("link", { name: "Bought" }).click();
  await page.getByRole("link", { name: "Add to stock as a new item" }).click();

  await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Chain lube");
  await expect(page.getByLabel("On stock")).toHaveValue("2");
  await page.getByLabel("Total price").fill("59,80");
  await page.getByLabel("Shop").fill("Moto shop");
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".flash-success")).toContainText("Added Chain lube to stock");
  await expect(page.locator(".list li").filter({ hasText: "Chain lube" })).toHaveCount(0);

  await page.goto("/stock");
  await row(page, "Chain lube").getByRole("link").first().click();
  await expect(page.locator("#item-qty")).toHaveText("2x");
  await expect(page.locator('dt:text-is("Last price") + dd')).toHaveText("29.90 EUR / piece");
  await expect(page.locator('dt:text-is("Purchases") + dd')).toHaveText("1");
});

test("edit and delete items", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  await page.goto("/stock?q=Fuel");
  await page.locator(".item-row a.item-name").first().click();
  await page.getByRole("link", { name: "Edit", exact: true }).click();
  await page.getByLabel("Location").fill("Garage shelf 2");
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved");
  await expect(page.locator("main")).toContainText("Garage shelf 2");

  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page).toHaveURL(/\/stock$/);
  await expect(page.locator(".flash-success")).toContainText("Deleted OEM Fuel Filter");
  await expect(page.locator("#results")).not.toContainText("Fuel Filter");
});

test("archive an item that a service still refers to", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  const filterRow = () => row(page, "OEM Oil Filter").filter({ hasNotText: "O-Ring" });
  await page.goto("/stock");
  await filterRow().getByRole("link").first().click();
  const itemURL = page.url();
  await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await page.getByRole("button", { name: "Archive" }).click();
  await expect(page.locator(".flash-success")).toContainText("Archived OEM Oil Filter");
  await expect(page.locator(".page-head")).toContainText("Archived");

  await page.goto("/stock");
  await expect(filterRow()).toHaveCount(0);

  // The logged oil service still lists it.
  await page.goto(itemURL);
  await page.locator('a[href^="/services/"]').first().click();
  await expect(page.locator("main")).toContainText("OEM Oil Filter");

  await page.goto(itemURL);
  await page.getByRole("button", { name: "Restore" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored OEM Oil Filter");
  await page.goto("/stock");
  await expect(filterRow()).toHaveCount(1);
});

test("fix a logged service", async ({ page }) => {
  await page.goto("/activity?kind=service");
  await page.locator('a[href^="/services/"]').first().click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil service");
  await page.getByRole("link", { name: "Edit", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Edit service");
  await page.getByLabel("Labour / workshop cost").fill("120");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved Oil service");
  await expect(page.locator('dt:text-is("Labour") + dd')).toHaveText("120.00 EUR");
  await expect(page.locator("main")).toContainText("1.7 L");
});

test("undo a deleted service", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  await page.goto("/stock");
  const before = await row(page, "15W50 PRO 4 XP").locator(".qty").textContent();
  await page.goto("/activity?kind=service");
  await page.locator('a[href^="/services/"]').first().click();
  await page.getByRole("button", { name: /Delete/ }).click();
  await expect(page.locator(".flash-success")).toContainText("Deleted Oil service");
  await page.locator(".flash").getByRole("button", { name: "Undo" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored Service: Oil service");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil service");
  await page.goto("/stock");
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText(before as string);
});

test("restore a shopping entry from the trash", async ({ page }) => {
  await page.goto("/shopping");
  await page.getByLabel("Something new").fill("Zip ties");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await page.getByRole("button", { name: "Remove Zip ties" }).click();
  await expect(page.locator(".list li").filter({ hasText: "Zip ties" })).toHaveCount(0);
  await page.goto("/activity");
  await page.locator("main").getByRole("link", { name: /^Trash/ }).click();
  const entry = page.locator(".list li").filter({ hasText: "Shopping list: Zip ties" });
  await entry.getByRole("button", { name: "Restore" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored Shopping list: Zip ties");
  await expect(page.locator(".list li").filter({ hasText: "Zip ties" })).toHaveCount(1);
  await page.getByRole("button", { name: "Remove Zip ties" }).click();
});

test("part rows keep a quantity hint until an item is chosen", async ({ page }) => {
  await page.goto("/services/new");
  const items = page.locator('select[name="part_item"]');
  const qtys = page.locator('input[name="part_qty"]');
  await expect(qtys.first()).toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
  await items.first().selectOption({ label: "OEM Oil Filter" });
  await expect(qtys.first()).not.toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
  await items.first().selectOption({ label: "Choose an item…" });
  await expect(qtys.first()).toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
});

test("part rows offer the chosen item's units", async ({ page }) => {
  await page.goto("/services/new");
  const row = page.locator(".part-line:not(.row-head)").first();
  const unit = row.locator('select[name="part_unit"]');
  await row.locator('select[name="part_item"]').selectOption({ label: "OEM Oil Filter" });
  await expect(unit).toHaveValue("pcs");
  await row.locator('select[name="part_item"]').selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await expect(unit).toHaveValue("l");
  await expect(row.locator(".stock-note")).toContainText("on stock:");
});

test("form hints open from the question mark", async ({ page }) => {
  await page.goto("/items/new");
  const hint = page.getByRole("tooltip", { name: "You get a low-stock warning below this." });
  await expect(hint).toBeHidden();
  // The input stays described by the hint for screen readers.
  await expect(page.getByLabel("Keep at least")).toHaveAccessibleDescription("You get a low-stock warning below this.");

  const help = page.locator(".label-row").filter({ hasText: "Keep at least" }).getByRole("button", { name: "Help" });
  await help.hover();
  await expect(hint).toBeVisible();
  await page.getByLabel("Name", { exact: true }).hover();
  await expect(hint).toBeHidden();

  // A click (or tap) keeps it open until Escape or a click elsewhere.
  await help.click();
  await page.getByLabel("Name", { exact: true }).hover();
  await expect(hint).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(hint).toBeHidden();
});

test("slash focuses search from any page", async ({ page }) => {
  await page.goto("/");
  await page.keyboard.press("/");
  await expect(page).toHaveURL(/\/stock$/);
  await expect(page.getByPlaceholder(/Search name/)).toBeFocused();

  await page.locator("main h1").click();
  await page.keyboard.press("/");
  await expect(page.getByPlaceholder(/Search name/)).toBeFocused();
});

test("switch between light and dark theme", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  const html = page.locator("html");
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const light = await bg();

  await page.getByRole("button", { name: "Switch to the dark theme" }).click();
  await expect(html).toHaveAttribute("data-theme", "dark");
  expect(await bg()).not.toBe(light);
  await page.reload();
  await expect(html).toHaveAttribute("data-theme", "dark");

  // Back to what the system shows: the choice is forgotten.
  await page.getByRole("button", { name: "Switch to the light theme" }).click();
  await expect(html).not.toHaveAttribute("data-theme");
  expect(await bg()).toBe(light);
  await page.reload();
  await expect(html).not.toHaveAttribute("data-theme");
  await expect(page.getByRole("button", { name: "Switch to the dark theme" })).toBeVisible();
});

test("Back shows the theme chosen on the next page", async ({ page, browserName }, testInfo) => {
  // Back shows the page as it was left, from the page cache or the HTTP
  // cache, with the theme it had then; it must switch to the one chosen.
  const check = async (p: Page, fromPageCache: boolean) => {
    await p.emulateMedia({ colorScheme: "light" });
    await p.goto("/stock");
    await p.evaluate(() => {
      window.addEventListener("pageshow", (e) => { (window as any).fromCache = e.persisted; });
    });
    await p.goto("/");
    await p.getByRole("button", { name: "Switch to the dark theme" }).click();
    await expect(p.locator("html")).toHaveAttribute("data-theme", "dark");
    await p.goBack({ waitUntil: "commit" });
    await expect(p).toHaveURL(/\/stock$/);
    if (fromPageCache) expect(await p.evaluate(() => (window as any).fromCache)).toBe(true);
    await expect(p.locator("html")).toHaveAttribute("data-theme", "dark");
    await expect(p.locator('meta[name="color-scheme"]')).toHaveAttribute("content", "dark");

    // And back to following the system.
    await p.goForward({ waitUntil: "commit" });
    await p.getByRole("button", { name: "Switch to the light theme" }).click();
    await expect(p.locator("html")).not.toHaveAttribute("data-theme");
    await p.goBack({ waitUntil: "commit" });
    await expect(p.locator("html")).not.toHaveAttribute("data-theme");
  };
  await check(page, false);

  // Playwright turns Chromium's page cache off, and its headless shell
  // has none; this full Chromium has it on.
  test.skip(browserName !== "chromium");
  const browser = await chromium.launch({ channel: "chromium", ignoreDefaultArgs: ["--disable-back-forward-cache"] });
  try {
    const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
    await check(await context.newPage(), true);
  } finally {
    await browser.close();
  }
});

test("Ctrl-click or middle-click on Copy as text opens the text", async ({ page, context }) => {
  await page.goto("/stock");
  const copy = page.getByRole("link", { name: "Copy as text" });
  for (const how of [{ modifiers: ["ControlOrMeta" as const] }, { button: "middle" as const }]) {
    const opened = context.waitForEvent("page");
    await copy.click(how);
    const tab = await opened;
    await tab.waitForLoadState();
    expect(tab.url()).toContain("/stock/export.txt");
    await expect(tab.locator("body")).toContainText("Silkolene 15W50 PRO 4 XP");
    await tab.close();
  }
  await expect(page).toHaveURL(/\/stock$/);
  await expect(copy).not.toHaveAttribute("data-copied");
});

test("replaced photos show their thumbnails in the trash", async ({ page }) => {
  // Three photos of one item, in different colours.
  const png = async (color: string) => Buffer.from(await page.evaluate((c) => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 64;
    const g = canvas.getContext("2d")!;
    g.fillStyle = c;
    g.fillRect(0, 0, 64, 64);
    return canvas.toDataURL("image/png").split(",")[1];
  }, color), "base64");
  await page.goto("/stock");
  await page.locator(".item-row a.item-name").filter({ hasText: "Hiflofiltro" }).click();
  const item = page.url();
  for (const [i, color] of ["#d32f2f", "#388e3c", "#1976d2"].entries()) {
    const file = { name: `p${i}.png`, mimeType: "image/png", buffer: await png(color) };
    await page.evaluate(() => { (window as any).before = true; });
    await page.locator('input[type="file"][name="photo"]').setInputFiles(file); // sends itself
    await page.waitForFunction(() => !(window as any).before); // the page after the upload
    await expect(page).toHaveURL(item);
    await expect(page.locator(".flash-success")).toContainText(i === 0 ? "Photo saved" : "Photo replaced");
  }
  // Undo the last replace: the second photo is back, the third in the trash.
  await page.locator(".flash").getByRole("button", { name: "Undo" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored the photo of Hiflofiltro Oil Filter. The newer photo is in the trash.");

  await page.goto("/trash");
  const photos = page.locator("li").filter({ hasText: "Photo: Hiflofiltro Oil Filter (replaced)" });
  await expect(photos).toHaveCount(2);
  const thumbs = photos.locator("img.thumb");
  await expect(thumbs).toHaveCount(2);
  const colours = [];
  for (const img of await thumbs.all()) {
    await expect(img).toHaveJSProperty("complete", true);
    expect(await img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
    colours.push(await img.evaluate((el: HTMLImageElement) => {
      const c = document.createElement("canvas");
      c.width = c.height = 1;
      c.getContext("2d")!.drawImage(el, 0, 0, 1, 1);
      return Array.from(c.getContext("2d")!.getImageData(0, 0, 1, 1).data.slice(0, 3));
    }));
  }
  // Newest first: the third (blue), then the first (red).
  expect(colours[0][2]).toBeGreaterThan(colours[0][0]);
  expect(colours[1][0]).toBeGreaterThan(colours[1][2]);
});

// A w x h RGB PNG, a gradient, made here so the test needs no files.
function gradientPNG(w: number, h: number): Buffer {
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    const row = y * (w * 3 + 1); // each row starts with filter type 0
    for (let x = 0; x < w; x++) raw.set([(x * 255) / w, (y * 255) / h, 128], row + 1 + x * 3);
  }
  const chunk = (type: string, data: Buffer) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(zlib.crc32(body));
    return Buffer.concat([len, body, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr.set([8, 2, 0, 0, 0], 8); // 8-bit RGB, not interlaced
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0)),
  ]);
}

// The width and height in a JPEG's or PNG's header.
function imageSize(b: Buffer): { type: string; w: number; h: number } {
  if (b.subarray(1, 4).toString("latin1") === "PNG") return { type: "png", w: b.readUInt32BE(16), h: b.readUInt32BE(20) };
  for (let i = 2; i + 9 < b.length; ) {
    const marker = b[i + 1];
    if (marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker)) {
      return { type: "jpeg", h: b.readUInt16BE(i + 5), w: b.readUInt16BE(i + 7) };
    }
    i += 2 + b.readUInt16BE(i + 2);
  }
  throw new Error("not a JPEG or PNG");
}

// The photo file in a multipart upload.
function uploadedPhoto(req: Request): Buffer {
  const body = req.postDataBuffer()!;
  const boundary = "--" + /boundary=(.+)$/.exec(req.headers()["content-type"])![1];
  const start = body.indexOf("\r\n\r\n", body.indexOf('name="photo"')) + 4;
  return body.subarray(start, body.indexOf("\r\n" + boundary, start));
}

test("a large photo is shrunk in the browser; without JavaScript the original goes", async ({ page, browser }, testInfo) => {
  const big = gradientPNG(2400, 1800);
  await page.goto("/stock");
  await page.locator(".item-row a.item-name").filter({ hasText: "Hiflofiltro" }).click();
  const item = page.url();
  const sent: Buffer[] = [];
  await page.route("**/items/*/photo", (route) => {
    if (route.request().method() === "POST") sent.push(uploadedPhoto(route.request()));
    return route.continue();
  });
  await page.evaluate(() => { (window as any).before = true; });
  await page.locator('input[type="file"][name="photo"]').setInputFiles({ name: "big.png", mimeType: "image/png", buffer: big });
  await page.waitForFunction(() => !(window as any).before);
  await expect(page.locator(".flash-success")).toContainText("Photo replaced");
  expect(sent).toHaveLength(1);
  expect(imageSize(sent[0])).toEqual({ type: "jpeg", w: 1600, h: 1200 });
  expect(sent[0].length).toBeLessThan(big.length);

  // Without JavaScript the form's Upload button sends the PNG as it is, and
  // the server shrinks it.
  const { baseURL, viewport } = testInfo.project.use;
  const context = await browser.newContext({ baseURL, viewport, javaScriptEnabled: false });
  try {
    const plain = await context.newPage();
    const sentPlain: Buffer[] = [];
    await plain.route("**/items/*/photo", (route) => {
      if (route.request().method() === "POST") sentPlain.push(uploadedPhoto(route.request()));
      return route.continue();
    });
    await plain.goto(item);
    await plain.locator('input[type="file"][name="photo"]').setInputFiles({ name: "big.png", mimeType: "image/png", buffer: big });
    await plain.getByRole("button", { name: "Upload" }).click();
    await expect(plain.locator(".flash-success")).toContainText("Photo replaced");
    expect(sentPlain).toHaveLength(1);
    expect(sentPlain[0].equals(big)).toBe(true);
    const photo = await context.request.get(new URL(item).pathname + "/photo");
    expect(imageSize(await photo.body())).toEqual({ type: "jpeg", w: 1600, h: 1200 });
  } finally {
    await context.close();
  }
});

test("no page scrolls sideways", async ({ page }) => {
  const paths = ["/", "/stock", "/stock?view=list", "/stock?sort=expiry&dir=desc", "/activity?sort=price", "/vehicles", "/services", "/shopping", "/activity", "/trash", "/items/new", "/kits/new", "/services/new"];
  // Also every detail page linked from the stock list and vehicles.
  await page.goto("/stock");
  paths.push(await page.locator(".item-row a.item-name").first().getAttribute("href") as string);
  await page.goto("/vehicles");
  paths.push(await page.locator("a.vehicle-card").first().getAttribute("href") as string);
  await page.goto("/activity");
  paths.push(await page.locator('a[href^="/movements/"]').first().getAttribute("href") as string);
  for (const p of paths) {
    await page.goto(p);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, `horizontal overflow on ${p}`).toBeLessThanOrEqual(0);
  }
});

// The item names of the table's rows, with repeats next to each other
// counted once.
async function columnRuns(page: Page, col: number) {
  const cells = await page.locator(`table.table tbody tr td:nth-child(${col})`).allInnerTexts();
  return cells.map((c) => c.trim()).filter((c, i, all) => i === 0 || c !== all[i - 1]);
}

test("sort the activity by clicking its headers", async ({ page }) => {
  await page.goto("/activity");
  const th = (name: string) => page.locator("table.table thead th").filter({ hasText: name });
  await expect(th("Date")).toHaveAttribute("aria-sort", "descending");
  await expect(th("Item")).toHaveAttribute("aria-sort", "none");

  await th("Item").getByRole("link").click();
  await expect(page).toHaveURL(/\/activity\?sort=item$/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "ascending");
  await expect(th("Date")).toHaveAttribute("aria-sort", "none");
  const az = await columnRuns(page, 2);
  expect(az.length).toBeGreaterThan(2);

  await th("Item").getByRole("link").click();
  await expect(page).toHaveURL(/\/activity\?dir=desc&sort=item$/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "descending");
  expect(await columnRuns(page, 2)).toEqual([...az].reverse());
  await expect(th("Item").getByRole("link")).toHaveAttribute("title", "Sort by Item, ascending");

  // A kind chip keeps the sort.
  await page.locator(".chips").getByRole("link", { name: "Purchase" }).click();
  await expect(page).toHaveURL(/dir=desc&kind=purchase&sort=item/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "descending");

  // Keyboard: the headers are links, with a visible focus ring.
  await th("Price").getByRole("link").focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/kind=purchase&sort=price/);
  await expect(th("Price")).toHaveAttribute("aria-sort", "descending");
  const cells = await page.locator("table.table tbody tr td:nth-child(5)").allInnerTexts();
  const prices = cells.filter((c) => c.trim()).map((p) => parseFloat(p.replace(/[^\d.]/g, "")));
  expect(prices.length).toBeGreaterThan(1);
  expect(prices).toEqual([...prices].sort((a, b) => b - a));
  // Purchases without a price come last.
  expect(cells.slice(prices.length).every((c) => !c.trim())).toBe(true);
});

test("sort the stock with Sort by and its direction", async ({ page }) => {
  await page.goto("/stock?view=list");
  const names = () => page.locator("#results .item-row .item-name").allInnerTexts();
  const az = await names();
  const sort = page.getByLabel("Sort by");
  await expect(sort).toHaveValue("name");
  const asc = page.locator("#sort-dir label").first();
  const desc = page.locator("#sort-dir label").last();
  await expect(asc).toHaveText("A–Z");
  await expect(page.locator("#dir-asc")).toBeChecked();

  // The direction flips the list, without a reload, and the address keeps it.
  await page.evaluate(() => { (window as any).same = true; });
  await desc.click();
  await expect(page).toHaveURL(/\/stock\?dir=desc&view=list$/);
  await expect.poll(names).toEqual([...az].reverse());
  await expect(page.locator("#dir-desc")).toBeChecked();
  expect(await page.evaluate(() => (window as any).same)).toBe(true);

  // Another key starts in its own direction, with its own labels.
  await sort.selectOption("recent");
  await expect(page).toHaveURL(/\/stock\?sort=recent&view=list$/);
  await expect(page.locator("#dir-desc")).toBeChecked();
  await expect(desc).toHaveText("Newest first");
  await expect(asc).toHaveText("Oldest first");
  // The item changed last comes first, and last the other way round.
  const screen = "Oil Screen 2 O-Ring";
  const before = await row(page, screen).locator(".qty").innerText();
  await row(page, screen).getByRole("button", { name: /^Add / }).click();
  await expect(row(page, screen).locator(".qty")).not.toHaveText(before);
  await page.reload();
  await expect(page.locator("#results .item-row").first()).toContainText(screen);
  await asc.click();
  await expect(page).toHaveURL(/\/stock\?dir=asc&sort=recent&view=list$/);
  await expect(page.locator("#results .item-row").last()).toContainText(screen);
  await row(page, screen).getByRole("button", { name: /^Use / }).click();
  await expect(row(page, screen).locator(".qty")).toHaveText(before);

  // Sorting isn't a filter, and the other links keep it.
  await expect(page.locator("#results")).not.toContainText("clear filters");
  await page.locator("#results").getByRole("link", { name: "Grouped" }).click();
  await expect(page).toHaveURL(/\/stock\?dir=asc&sort=recent$/);
  await expect(page.getByLabel("Sort by")).toHaveValue("recent");
});

test("sorting works without JavaScript", async ({ browser }, testInfo) => {
  const { baseURL, viewport } = testInfo.project.use;
  const context = await browser.newContext({ baseURL, viewport, javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto("/activity");
    const date = page.locator("table.table thead th").filter({ hasText: "Date" });
    await date.getByRole("link").click();
    await expect(page).toHaveURL(/\/activity\?dir=asc$/);
    await expect(date).toHaveAttribute("aria-sort", "ascending");

    // The stock form sends the sort with Apply, and lands on a plain URL.
    await page.goto("/stock");
    await page.getByLabel("Sort by").selectOption("expiry");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page).toHaveURL(/\/stock\?sort=expiry$/);
    await expect(page.getByLabel("Sort by")).toHaveValue("expiry");
    await expect(page.locator("#dir-asc")).toBeChecked();
    await expect(page.locator("#sort-dir")).toContainText("Soonest first");
    await page.locator("#sort-dir label").last().click();
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page).toHaveURL(/\/stock\?dir=desc&sort=expiry$/);
  } finally {
    await context.close();
  }
});

// Opt-in: SCREENSHOT_DIR=/tmp/shots npx playwright test -g screenshots (see docs/development.md)
test("screenshots", async ({ page }, testInfo) => {
  const dir = process.env.SCREENSHOT_DIR;
  test.skip(!dir, "set SCREENSHOT_DIR to capture screenshots");
  // SCREENSHOT_SCHEME=dark for the dark theme (the names get "-dark").
  const scheme = process.env.SCREENSHOT_SCHEME === "dark" ? "dark" : "light";
  await page.emulateMedia({ colorScheme: scheme });
  const suffix = scheme === "dark" ? "-dark" : "";
  const shots: [string, string][] = [
    ["dashboard", "/"],
    ["stock", "/stock"],
    ["vehicles", "/vehicles"],
    ["services", "/services"],
    ["shopping", "/shopping"],
    ["activity", "/activity"],
    ["trash", "/trash"],
    ["activity-sorted", "/activity?sort=price"],
    ["stock-sorted", "/stock?sort=expiry&dir=desc"],
    ["item-new", "/items/new"],
  ];
  await page.goto("/stock?q=Silkolene");
  shots.push(["item", (await page.locator(".item-row a.item-name").first().getAttribute("href")) as string]);
  shots.push(["item-edit", shots[shots.length - 1][1] + "/edit"]);
  await page.goto("/vehicles");
  const vehicle = (await page.locator("a.vehicle-card").first().getAttribute("href")) as string;
  shots.push(["vehicle", vehicle]);
  await page.goto("/services");
  shots.push(["kit", (await page.locator("a.kit-row").first().getAttribute("href")) as string]);
  shots.push(["kit-sorted", shots[shots.length - 1][1] + "?sort=missing"]);
  shots.push(["kit-new", "/kits/new"]);
  shots.push(["service-new", "/services/new"]);
  for (const [name, url] of shots) {
    await page.goto(url);
    // A full-page shot keeps the fixed tab bar where the first screen ends,
    // over the content: put it at the end of the page, as when scrolled down.
    await page.addStyleTag({ content: "body { position: relative; } .tabbar { position: absolute !important; }" });
    await page.screenshot({ path: `${dir}/${testInfo.project.name}-${name}${suffix}.png`, fullPage: true });
  }
});

test("an edit saved in another tab first is not overwritten", async ({ page, context }) => {
  page.on("dialog", (d) => d.accept());
  await page.goto("/stock");
  await row(page, "OEM Rear Brake Pads").getByRole("link").first().click();
  const edit = page.url() + "/edit";
  await page.goto(edit);

  const other = await context.newPage();
  other.on("dialog", (d) => d.accept());
  await other.goto(edit);
  await other.getByLabel("Notes").fill("From the other tab");
  await other.getByRole("button", { name: "Save item" }).click();
  await expect(other.locator(".flash-success")).toContainText("Saved");

  // The first tab is refused, keeps what was typed, and offers a reload.
  await page.getByLabel("Notes").fill("From this tab");
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".error-summary")).toContainText("This item was changed since you opened this");
  await expect(page.getByLabel("Notes")).toHaveValue("From this tab");
  await page.getByRole("link", { name: "Reload" }).click();
  await expect(page.getByLabel("Notes")).toHaveValue("From the other tab");

  // Deleted and undone in the other tab: the form opened before is stale.
  await other.goto(edit.replace(/\/edit$/, ""));
  const archive = other.getByRole("button", { name: "Archive" });
  if (await archive.count()) {
    await archive.click();
    await other.getByRole("button", { name: "Restore" }).click();
  } else {
    await other.getByRole("button", { name: "Delete" }).click();
    await other.locator(".flash").getByRole("button", { name: "Undo" }).click();
  }
  await expect(other.locator(".flash-success")).toContainText("Restored");
  await page.getByLabel("Notes").fill("");
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".error-summary")).toContainText("was changed since you opened this");
  await page.getByRole("link", { name: "Reload" }).click();
  await page.getByLabel("Notes").fill("");
  await page.getByRole("button", { name: "Save item" }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved");
  await other.close();
});

test("a save that seems stuck can be tried again, and saves once", async ({ page, context }) => {
  test.setTimeout(60_000); // "try again" comes after 15 s, waited for real
  // Chromium holds DevTools commands to a page while it navigates, so the
  // form is opened in a window of this page, which watches it and clicks
  // its button while the save is held.
  await page.goto("/services");
  const [form] = await Promise.all([
    context.waitForEvent("page"),
    page.evaluate(() => { (window as any).held = window.open("/services/new"); }),
  ]);
  await form.waitForLoadState();
  await form.getByLabel("Vehicle", { exact: true }).selectOption({ label: "KTM 390" });
  await form.getByLabel("What was done").fill("Chain clean (retried)");

  // The first save is held on its way to the server. Once let go it is
  // saved, and only then does the retry arrive, as after a slow network.
  let release = () => {};
  const released = new Promise<void>((r) => (release = r));
  let firstSaved = () => {};
  const saved = new Promise<void>((r) => (firstSaved = r));
  let posts = 0;
  await context.route("**/services", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    if (++posts === 1) {
      await released;
      // The browser gave up on it for the retry; it arrives anyway.
      await page.request.fetch(route.request(), { maxRedirects: 0 });
      firstSaved();
      await route.abort().catch(() => {});
      return;
    }
    await saved;
    await route.continue();
  });

  const save = 'form[action="/services"] button.btn-primary';
  const label = () => page.evaluate((s) => (window as any).held.document.querySelector(s).textContent.trim(), save);
  const click = () => page.evaluate((s) => (window as any).held.document.querySelector(s).click(), save);
  await click();
  await expect.poll(label).toBe("Saving…");
  await expect.poll(label, { timeout: 20_000 }).toBe("Still saving… try again");
  await click();
  await expect.poll(label).toBe("Saving…");
  release();

  await expect(form).toHaveURL(/\/services\/\d+$/);
  await expect(form.locator(".flash-success")).toContainText("This was saved already");
  expect(posts).toBe(2);
  await context.unroute("**/services");
  await form.close();
  await page.goto("/services");
  await expect(page.getByRole("link", { name: "Chain clean (retried)" })).toHaveCount(1);
});

// Last, so the item it adds doesn't change what the tests above see.
test("Enter in the new item form saves the item", async ({ page }) => {
  await page.goto("/items/new");
  await page.getByLabel("Name", { exact: true }).fill("Brake cleaner");
  await page.getByLabel("Name", { exact: true }).press("Enter");
  await expect(page).toHaveURL(/\/items\/\d+$/);
  await expect(page.locator("h1")).toContainText("Brake cleaner");
});
