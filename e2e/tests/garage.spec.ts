import { chromium, expect, test, type Locator, type Page, type Request, type TestInfo } from "@playwright/test";
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
  skus?: { code: string; kind?: string; brand?: string; pack?: string; packUnit?: "pcs" | "ml" | "l" | "g" | "kg" }[];
};

const exact = (s: string) => new RegExp(`^\\s*${s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);

// The projects (playwright.config.ts): a phone has the tab bar and its
// sheets; desktop and laptop the sidebar.
const phone = (t: TestInfo) => !!t.project.use.isMobile;

// A field by its label. Exact, since a label's "?" hint is a button named
// "More about <label>", which a loose match finds too.
const field = (scope: Page | Locator, label: string) => scope.getByLabel(label, { exact: true });

async function addVehicle(page: Page, name: string, odometer: string) {
  await page.goto("/vehicles/new");
  await field(page, "Name").fill(name);
  await page.getByLabel("Odometer (km)").fill(odometer);
  await page.getByRole("button", { name: "Save vehicle" }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
}

async function addItem(page: Page, it: ItemSpec) {
  await page.goto("/items/new");
  await page.getByLabel("Name", { exact: true }).fill(it.name);
  if (it.brand) await page.getByLabel("Brand", { exact: true }).fill(it.brand);
  if (it.volume) await page.locator("label", { hasText: "Volume (ml / L)" }).click();
  await page.getByLabel("In stock").fill(it.qty);
  if (it.qtyUnit) await page.locator('select[name="quantity_unit"]').selectOption(it.qtyUnit);
  if (it.min) await field(page, "Keep at least").fill(it.min);
  if (it.category) await page.getByLabel("Category").fill(it.category);
  for (const v of it.vehicles) {
    await page.locator("label.chip-toggle").filter({ hasText: exact(v) }).click();
  }
  for (const [i, sku] of (it.skus ?? []).entries()) {
    if (i > 0) await page.getByRole("button", { name: "Add SKU" }).click();
    await page.locator('input[name="sku_code"]').nth(i).fill(sku.code);
    if (sku.kind) await page.locator('select[name="sku_kind"]').nth(i).selectOption(sku.kind);
    if (sku.brand) await page.locator('input[name="sku_brand"]').nth(i).fill(sku.brand);
    if (sku.pack) await page.locator('input[name="sku_pack"]').nth(i).fill(sku.pack);
    if (sku.packUnit) await page.locator('select[name="sku_pack_unit"]').nth(i).selectOption(sku.packUnit);
  }
  await page.getByRole("button", { name: "Save item", exact: true }).click();
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
    { brand: "KTM", name: "Rear Brake Pads", qty: "1", vehicles: ["KTM 390"], skus: [{ code: "90113090000", brand: "KTM" }] },
    { brand: "KTM", name: "Fuel Filter", qty: "1", vehicles: ["KTM 390"] },
    { brand: "KTM", name: "Air Filter", qty: "1", vehicles: ["KTM 390"] },
    { brand: "KTM", name: "Oil Filter", qty: "1", min: "1", vehicles: ["KTM 390"], skus: [{ code: "90138015000", brand: "KTM" }] },
    { brand: "KTM", name: "Oil Screen 1 O-Ring", qty: "2", vehicles: ["KTM 390"] },
    { brand: "KTM", name: "Oil Screen 2 O-Ring", qty: "3", vehicles: ["KTM 390"] },
    {
      brand: "Silkolene", name: "15W50 PRO 4 XP", volume: true, qty: "525", qtyUnit: "ml", vehicles: ["KTM 390"], category: "Oil & Fluids",
      skus: [
        { code: "SK-80085", kind: "manufacturer", brand: "Silkolene", pack: "1", packUnit: "l" },
        { code: "SK-80086", kind: "manufacturer", brand: "Silkolene", pack: "4", packUnit: "l" },
        { code: "SK-80087", kind: "manufacturer", brand: "Silkolene", pack: "20", packUnit: "l" },
        { code: "SK-80088", kind: "manufacturer", brand: "Silkolene", pack: "60", packUnit: "l" },
      ],
    },
  ];
  const ktm1290: ItemSpec[] = [
    { brand: "KTM", name: "Rear Brake Pads (1290)", qty: "1", vehicles: ["KTM 1290"] },
    { brand: "Hiflofiltro", name: "Oil Filter", qty: "1", vehicles: ["KTM 1290"], skus: [{ code: "HF-650", kind: "manufacturer", brand: "Hiflofiltro" }] },
    { brand: "KTM", name: "Oil Screen 1+2 O-Ring", qty: "6", vehicles: ["KTM 1290"] },
    { brand: "Motorex", name: "10W50 Power Synt 4T", volume: true, qty: "1.5", qtyUnit: "l", vehicles: ["KTM 1290"] },
  ];
  const both: ItemSpec[] = [
    { brand: "KTM", name: "Oil Filter O-Ring", qty: "6", vehicles: ["KTM 390", "KTM 1290"] },
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
  expect(text).toContain("KTM 390 + KTM 1290\n------------------\n6 pcs - KTM Oil Filter O-Ring\n640 ml - Motorex M3.0 Ready To Use");
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

  // Only two part numbers show in a row; "+2 more" reveals the rest in place.
  const silk = page.locator(".item-row").filter({ hasText: "Silkolene" });
  await page.goto("/stock");
  await expect(silk.locator(".sku:visible")).toHaveCount(2);
  await silk.getByRole("link", { name: "Show 2 more SKUs" }).click();
  await expect(page).toHaveURL(/\/stock$/);
  await expect(silk.locator(".sku:visible")).toHaveCount(4);
  await expect(silk.getByRole("link", { name: "SK-80087" })).toBeFocused();

  // The category tag sits on the row and links to the filtered stock.
  await silk.getByRole("link", { name: "Oil & Fluids" }).click();
  await expect(page).toHaveURL(/cat=Oil(\+|%20)%26(\+|%20)Fluids/);
  await expect(page.locator(".item-row")).toHaveCount(1);

  // So do the row's vehicle chips (shown in the list view), like the sidebar's.
  await page.goto("/stock?view=list");
  await silk.getByRole("link", { name: "KTM 390", exact: true }).click();
  await expect(page).toHaveURL(/\/stock\?v=\d+$/);
  await expect(page.locator("h1")).toHaveText("KTM 390 stock");
  await expect(silk).toBeVisible();
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
  const bought = page.locator("#panel-bought");
  await bought.getByLabel("Amount bought").fill("1");
  await bought.locator('select[name="amount_unit"]').selectOption("l");
  await field(bought, "Total price (EUR)").fill("18,50");
  await bought.getByLabel("Shop").fill("Moto shop");
  await bought.getByRole("button", { name: "Add to stock" }).click();
  await expect(page.locator("#item-qty")).toHaveText("1\u202f525 ml");
  await expect(page.locator("section").filter({ hasText: "History" }).locator("table")).toContainText("Moto shop");
});

test("fix a wrong price in the history", async ({ page }) => {
  await page.goto("/stock");
  await row(page, "15W50 PRO 4 XP").getByRole("link").first().click();
  const history = page.locator("section").filter({ hasText: "History" });
  await history.locator("tr").filter({ hasText: "Moto shop" }).getByRole("link", { name: /Edit the purchase/ }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Edit purchase");
  await expect(field(page, "Total price (EUR)")).toHaveValue("18.50");
  await field(page, "Total price (EUR)").fill("16,50");
  await page.getByRole("button", { name: "Save purchase" }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved the purchase");
  await expect(page.locator("#item-qty")).toHaveText("1\u202f525 ml");
  await expect(page.locator('dt:text-is("Last price") + dd')).toHaveText("16.50 EUR / L");
});

test("plan an oil service, shop for what is missing and log it", async ({ page }, testInfo) => {
  await page.goto("/vehicles");
  await page.locator("a.vehicle-card").filter({ has: page.locator(".name", { hasText: exact("KTM 390") }) }).click();
  await page.getByRole("link", { name: "Add kit" }).click();

  await field(page, "Name").fill("Oil Service");
  await page.getByLabel("Distance (km)", { exact: true }).fill("7500");
  await page.getByLabel("Months", { exact: true }).fill("12");
  const items = page.locator('select[name="line_item"]');
  const qtys = page.locator('input[name="line_qty"]');
  await items.nth(0).selectOption({ label: "KTM Oil Filter" });
  await qtys.nth(0).fill("1");
  // One empty row to start with; Add part adds the next.
  await page.getByRole("button", { name: "Add part" }).first().click();
  await items.nth(1).selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await qtys.nth(1).fill("1.7 L");
  await page.getByRole("button", { name: "Add part" }).first().click();
  await items.nth(2).selectOption({ label: "KTM Oil Screen 1 O-Ring" });
  await qtys.nth(2).fill("1");
  await page.getByRole("button", { name: "Save kit" }).click();

  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil Service");
  await expect(page.locator(".page-head")).toContainText("1 part missing");
  await expect(page.locator(".page-head")).toContainText("Not done yet");
  // Each part's stock against what the kit needs is a <meter>, red when short.
  const meters = page.locator("meter.meter");
  await expect(meters).toHaveCount(3);
  await expect(page.locator("meter.meter.short")).toHaveCount(1);
  const short = page.locator("meter.meter.short");
  expect(Number(await short.getAttribute("value"))).toBeLessThan(Number(await short.getAttribute("max")));
  await page.screenshot({ path: testInfo.outputPath("kit-short.png"), fullPage: true });

  await expect(page.locator(".page-head")).not.toContainText("Add missing");
  await page.getByRole("button", { name: "Add 1 missing" }).click();
  await expect(page.locator(".flash-success")).toContainText("missing");
  await expect(page.locator(".card-actions").getByRole("link", { name: "On shopping list" })).toBeVisible();
  await page.goto("/shopping");
  const entry = page.locator(".list li").filter({ hasText: "Silkolene" }).first();
  await expect(entry).toContainText("175 ml");
  await entry.getByRole("link", { name: "Bought" }).click();
  await page.getByLabel("Amount bought").fill("4");
  await page.locator('select[name="amount_unit"]').selectOption("l");
  await page.getByRole("button", { name: "Add to stock" }).click();
  await expect(page.locator(".flash-success")).toContainText("Bought 4 L of Silkolene");

  await page.goto("/services");
  await page.getByRole("link", { name: /Oil Service/ }).first().click();
  await expect(page.locator(".page-head")).toContainText("Parts ready");
  await page.locator(".page-head").getByRole("link", { name: "Log service" }).click();
  await page.getByLabel("Odometer (km)").fill("12000");
  await page.getByRole("button", { name: "Save service" }).click();

  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil Service");
  await expect(page.locator("main")).toContainText("1.7 L");
  await expect(page.locator("main")).toContainText("12\u202f000 km");

  await page.goto("/stock");
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("3\u202f825 ml");
  await expect(row(page, "KTM Oil Filter").filter({ hasNotText: "O-Ring" }).locator(".qty")).toHaveText("0 pcs");

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
  await expect(page.getByLabel("In stock")).toHaveValue("2");
  await field(page, "Total price (EUR)").fill("59,80");
  await page.getByLabel("Shop").fill("Moto shop");
  await page.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(page.locator(".flash-success")).toContainText("Added Chain lube to stock");
  await expect(page.locator(".list li").filter({ hasText: "Chain lube" })).toHaveCount(0);

  await page.goto("/stock");
  await row(page, "Chain lube").getByRole("link").first().click();
  await expect(page.locator("#item-qty")).toHaveText("2 pcs");
  await expect(page.locator('dt:text-is("Last price") + dd')).toHaveText("29.90 EUR / piece");
  await expect(page.locator('dt:text-is("Purchases") + dd')).toHaveText("1");
});

test("edit and delete items", async ({ page }) => {
  await page.goto("/stock?q=Fuel");
  await page.locator(".item-row a.item-name").first().click();
  await page.getByRole("link", { name: "Edit", exact: true }).click();
  await page.getByLabel("Location").fill("Garage shelf 2");
  await page.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved");
  await expect(page.locator("main")).toContainText("Garage shelf 2");

  await page.getByRole("button", { name: "Delete" }).click();
  await expect(page).toHaveURL(/\/stock$/);
  await expect(page.locator(".flash-success")).toContainText("Deleted KTM Fuel Filter");
  await expect(page.locator("#results")).not.toContainText("Fuel Filter");
});

test("archive an item that a service still refers to", async ({ page }) => {
  const filterRow = () => row(page, "KTM Oil Filter").filter({ hasNotText: "O-Ring" });
  await page.goto("/stock");
  await filterRow().getByRole("link").first().click();
  const itemURL = page.url();
  await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await page.getByRole("button", { name: "Archive" }).click();
  await expect(page.locator(".flash-success")).toContainText("Archived KTM Oil Filter");
  await expect(page.locator(".page-head")).toContainText("Archived");

  await page.goto("/stock");
  await expect(filterRow()).toHaveCount(0);

  // The logged oil service still lists it.
  await page.goto(itemURL);
  await page.locator('main a[href^="/services/"]').first().click();
  await expect(page.locator("main")).toContainText("KTM Oil Filter");

  await page.goto(itemURL);
  await page.getByRole("button", { name: "Restore" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored KTM Oil Filter");
  await page.goto("/stock");
  await expect(filterRow()).toHaveCount(1);
});

test("fix a logged service", async ({ page }) => {
  await page.goto("/activity?kind=service");
  await page.locator('main a[href^="/services/"]').first().click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil Service");
  await page.getByRole("link", { name: "Edit", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Edit service");
  await page.getByLabel("Labour cost").fill("120");
  await page.getByRole("button", { name: "Save service" }).click();
  await expect(page.locator(".flash-success")).toContainText("Saved Oil Service");
  await expect(page.locator('dt:text-is("Labour") + dd')).toHaveText("120.00 EUR");
  await expect(page.locator("main")).toContainText("1.7 L");
});

test("undo a deleted service", async ({ page }) => {
  await page.goto("/stock");
  const before = await row(page, "15W50 PRO 4 XP").locator(".qty").textContent();
  await page.goto("/activity?kind=service");
  await page.locator('main a[href^="/services/"]').first().click();
  await page.getByRole("button", { name: /Delete/ }).click();
  await expect(page.locator(".flash-success")).toContainText("Deleted Oil Service");
  await page.locator(".flash").getByRole("button", { name: "Undo" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored Oil Service");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Oil Service");
  await page.goto("/stock");
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText(before as string);
});

test("restore a shopping entry from the trash", async ({ page }) => {
  await page.goto("/shopping");
  await page.getByLabel("Something new").fill("Zip ties");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await page.getByRole("button", { name: "Remove Zip ties" }).click();
  await expect(page.locator(".list li").filter({ hasText: "Zip ties" })).toHaveCount(0);
  // The sidebar's Trash (on a phone, under More).
  await (await sidebar(page)).getByRole("link", { name: /^Trash/ }).click();
  const entry = page.locator(".list li").filter({ hasText: "Shopping list: Zip ties" });
  await entry.getByRole("button", { name: "Restore" }).click();
  await expect(page.locator(".flash-success")).toContainText("Restored Zip ties to the shopping list");
  await expect(page.locator(".list li").filter({ hasText: "Zip ties" })).toHaveCount(1);
  await page.getByRole("button", { name: "Remove Zip ties" }).click();
});

test("a permanent delete asks in a dialog first", async ({ page }) => {
  // The test above left Zip ties in the trash.
  await page.goto("/trash");
  const entry = page.locator(".list li").filter({ hasText: "Shopping list: Zip ties" });
  await expect(entry).toHaveCount(1);
  const ask = entry.getByRole("button", { name: /^Delete .*Zip ties permanently$/ });
  const dialog = page.getByRole("dialog", { name: /Zip ties permanently\?$/ });
  await expect(dialog).toBeHidden();

  // Its button opens it (no script), Cancel first, so Enter doesn't delete.
  await ask.click();
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("It can't be restored after that.");
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  // Esc, Cancel and a click outside it each keep the entry.
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await ask.click();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  await ask.click();
  await expect(dialog).toBeVisible();
  await page.mouse.click(5, 5);
  await expect(dialog).toBeHidden();
  await expect(entry).toHaveCount(1);

  await ask.click();
  await dialog.getByRole("button", { name: "Delete permanently" }).click();
  await expect(page.locator(".flash-success")).toContainText("Permanently deleted");
  await expect(entry).toHaveCount(0);
});

test("part rows keep a quantity hint until an item is chosen", async ({ page }) => {
  await page.goto("/services/new");
  const items = page.locator('select[name="part_item"]');
  const qtys = page.locator('input[name="part_qty"]');
  await expect(qtys.first()).toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
  await items.first().selectOption({ label: "KTM Oil Filter" });
  await expect(qtys.first()).not.toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
  await items.first().selectOption({ label: "Choose an item…" });
  await expect(qtys.first()).toHaveAttribute("placeholder", "e.g. 1 or 1.7 L");
});

test("part rows offer the chosen item's units", async ({ page }) => {
  await page.goto("/services/new");
  const row = page.locator(".part-line:not(.row-head)").first();
  const unit = row.locator('select[name="part_unit"]');
  await row.locator('select[name="part_item"]').selectOption({ label: "KTM Oil Filter" });
  await expect(unit).toHaveValue("pcs");
  await row.locator('select[name="part_item"]').selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await expect(unit).toHaveValue("l");
  await expect(row.locator("[data-stock-badge] .badge")).toContainText("in stock");
});

test("part rows show the item's stock badge, red when the rows ask for more", async ({ page }) => {
  await page.goto("/services/new");
  const rows = page.locator("#part-lines .part-line:not(.row-head)");
  const badge = (i: number) => rows.nth(i).locator("[data-stock-badge] .badge");
  const first = rows.nth(0);
  await first.locator('select[name="part_item"]').selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  const have = Number(await first.locator('select[name="part_item"] option:checked').getAttribute("data-have-base"));
  expect(have).toBeGreaterThan(2);
  await expect(badge(0)).toHaveAttribute("data-stock-level", /^(ok|low)$/);

  // Half of it in ml is there; the same number in L isn't.
  const half = Math.floor(have / 2);
  await first.locator('select[name="part_unit"]').selectOption("ml");
  await first.locator('input[name="part_qty"]').fill(String(half));
  await expect(badge(0)).toHaveAttribute("data-stock-level", /^(ok|low)$/);
  await first.locator('select[name="part_unit"]').selectOption("l");
  await expect(badge(0)).toHaveAttribute("data-stock-level", "short");
  await expect(badge(0)).toHaveAttribute("title", /^Need \d/);
  await first.locator('select[name="part_unit"]').selectOption("ml");
  await expect(badge(0)).toHaveAttribute("data-stock-level", /^(ok|low)$/);

  // Two rows of the same item count together, and removing one frees it.
  await expect(rows).toHaveCount(1);
  await page.getByRole("button", { name: "Add part" }).click();
  const second = rows.nth(1);
  await second.locator('select[name="part_item"]').selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await second.locator('select[name="part_unit"]').selectOption("ml");
  await second.locator('input[name="part_qty"]').fill(String(have - half + 1));
  await expect(badge(0)).toHaveAttribute("data-stock-level", "short");
  await expect(badge(1)).toHaveAttribute("data-stock-level", "short");
  await second.locator("[data-remove-row]").click();
  await expect(rows).toHaveCount(1);
  await expect(badge(0)).toHaveAttribute("data-stock-level", /^(ok|low)$/);
});

test("Log service offers the chosen vehicle's kits and keeps what was typed", async ({ page }) => {
  await page.goto("/services/new");
  const starts = page.locator("#kit-starts");
  const odometer = page.getByLabel("Odometer (km)");
  await expect(starts).toBeHidden();
  await expect(odometer).toHaveAttribute("placeholder", "e.g. 15000");
  await page.locator("#vehicle_id").selectOption({ label: "KTM 390" });
  await expect(starts).toBeVisible();
  // The odometer stays empty; the vehicle's last reading is its placeholder.
  await expect(odometer).toHaveValue("");
  await expect(odometer).toHaveAttribute("placeholder", /^Last reading \d/);
  const kit = starts.getByRole("link", { name: "Oil Service" });
  await expect(kit).toBeVisible();
  expect([...(await offGrid(page)), ...(await misaligned(page))]).toEqual([]);

  // Untouched, the kit's own link.
  await kit.click();
  await expect(page).toHaveURL(/\/services\/new\?kit=\d+$/);
  await expect(page.locator("#kit-starts")).toHaveCount(0);

  // What was changed comes along: the date, odometer and labour.
  await page.goto("/services/new");
  await page.locator("#vehicle_id").selectOption({ label: "KTM 390" });
  await page.getByLabel("Done on", { exact: true }).fill("2026-10-05");
  await odometer.fill("12345");
  await page.getByLabel(/^Labour cost/).fill("80");
  await kit.click();
  await expect(page).toHaveURL(/\/services\/new\?kit=\d+&performed_on=2026-10-05&odometer=12345&labor_cost=80$/);
  await expect(page.getByLabel("Work performed")).toHaveValue(/^Oil Service/);
  await expect(page.getByLabel("Done on", { exact: true })).toHaveValue("2026-10-05");
  await expect(odometer).toHaveValue("12345");
  await expect(page.getByLabel(/^Labour cost/)).toHaveValue("80");
});

test("form hints open from the question mark", async ({ page }) => {
  await page.goto("/items/new");
  const hint = page.getByRole("tooltip", { name: "You get a low-stock warning below this." });
  await expect(hint).toBeHidden();
  // The input stays described by the hint for screen readers, and the "?"
  // says which field it is about.
  await expect(field(page, "Keep at least")).toHaveAccessibleDescription("You get a low-stock warning below this.");
  const help = page.getByRole("button", { name: "More about Keep at least" });

  // Hover shows it, as a popover in the top layer (no card clips it), 6px
  // under its "?".
  await help.hover();
  await expect(hint).toBeVisible();
  expect(await hint.evaluate((e) => e.matches(":popover-open"))).toBe(true);
  const b = (await help.boundingBox())!, h = (await hint.boundingBox())!;
  expect(Math.round(h.y - (b.y + b.height)), "the hint's distance under its button").toBe(6);
  await field(page, "Name").hover();
  await expect(hint).toBeHidden();

  // So does keyboard focus, for as long as it lasts.
  await help.focus();
  await expect(hint).toBeVisible();
  await field(page, "Keep at least").focus();
  await expect(hint).toBeHidden();

  // A click (or tap) keeps it open until Escape or a click elsewhere.
  await help.click();
  await field(page, "Name").hover();
  await expect(hint).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(hint).toBeHidden();
  await help.click();
  await expect(hint).toBeVisible();
  await page.locator("main h1").click();
  await expect(hint).toBeHidden();
});

test("a form sent back names its errors at the fields", async ({ page }) => {
  await page.goto("/vehicles/new");
  const name = field(page, "Name");
  await expect(name).toHaveAttribute("required", "");
  await expect(name).toHaveAttribute("aria-invalid", "false");
  await page.getByLabel("Year").fill("soon");
  await page.getByRole("button", { name: "Save vehicle" }).click();
  // The server's answer: a summary up top (the sentence alone; each message
  // is only at its field), and the field marked, focused and described by
  // its own message (and still by its hint).
  await expect(page.locator(".error-summary")).toHaveText("Fix the fields marked below.");
  await expect(page.locator(".error-summary a")).toHaveCount(0);
  await expect(name).toBeFocused();
  await expect(name).toHaveAttribute("aria-invalid", "true");
  const msg = (await page.locator("#name-err").innerText()).trim();
  expect(msg).toMatch(/^[A-Z]/);
  await expect(name).toHaveAccessibleDescription(new RegExp(`^${msg.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`));
  await expect(name).toHaveAccessibleDescription(/Keep it short/);
});

test("a form's sections have a line between them, and none above the first", async ({ page }, testInfo) => {
  // The line was "a section right after a section": hidden inputs between
  // the error banner and the fields (a service from a kit, a kit for the
  // vehicle worked on) dropped it, and the hidden "Start from a kit" put
  // one over the first section of Log service.
  const lines = () => page.evaluate(() => {
    const out: string[] = [];
    for (const form of document.querySelectorAll("form.card")) {
      const shown = [...form.querySelectorAll(":scope > .form-section")].filter((s) => s.getBoundingClientRect().height > 0);
      shown.forEach((s, i) => {
        const top = parseFloat(getComputedStyle(s).borderTopWidth);
        const what = (s.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 30);
        if (i === 0 && top !== 0) out.push(`a line over the first section ("${what}")`);
        if (i > 0 && top !== 1) out.push(`no line over section ${i + 1} ("${what}")`);
      });
    }
    return out;
  });
  const problems: string[] = [];
  await page.goto("/services/new");
  await expect(page.locator("#kit-starts")).toBeHidden();
  problems.push(...(await lines()).map((p) => `Log service: ${p}`));
  await page.locator("#vehicle_id").selectOption({ label: "KTM 390" });
  await page.locator("#kit-starts").getByRole("link", { name: "Oil Service" }).click();
  await expect(page).toHaveURL(/\/services\/new\?kit=\d+$/);
  await page.getByLabel("Done on", { exact: true }).fill("");
  await page.getByRole("button", { name: "Save service" }).click();
  await expect(page.locator(".error-summary")).toBeVisible();
  problems.push(...(await lines()).map((p) => `Log service from a kit, with errors: ${p}`));
  await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
  await page.goto("/kits/new");
  await page.getByRole("button", { name: "Save kit" }).click();
  await expect(page.locator(".error-summary")).toBeVisible();
  problems.push(...(await lines()).map((p) => `Add kit working on a vehicle, with errors: ${p}`));
  expect(problems).toEqual([]);
});

test("a clicked field shows a 2px focus ring, red with an error, and an open select keeps it", async ({ page }) => {
  await page.goto("/shopping");
  const colour = (v: string) => page.evaluate((v) => {
    const probe = document.createElement("div");
    probe.style.color = `var(${v})`;
    document.body.append(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  }, v);
  const accent = await colour("--accent");
  // The border and a 1px shadow in the ring colour, then the pale glow.
  const ring = (c: string) => new RegExp(`^${c.replace(/[()]/g, "\\$&")} 0px 0px 0px 1px, `);

  const name = field(page, "Something new");
  await name.click();
  await expect(name).toHaveCSS("border-top-color", accent);
  await expect(name).toHaveCSS("box-shadow", ring(accent));

  const unit = page.locator('select[name="quantity_unit"]');
  await unit.click();
  // Open (the themed list), it keeps the ring.
  await expect.poll(() => unit.evaluate((e) => e.matches(":open"))).toBe(true);
  await expect(unit).toHaveCSS("border-top-color", accent);
  await expect(unit).toHaveCSS("box-shadow", ring(accent));
  await page.keyboard.press("Escape");

  // A field with an error rings red, not purple around a red border.
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.locator(".has-error").getByLabel("Something new")).toBeVisible();
  await name.click();
  const danger = await colour("--danger");
  await expect(name).toHaveCSS("border-top-color", danger);
  await expect(name).toHaveCSS("box-shadow", ring(danger));
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

test("a and l open Add item and Log service from any page", async ({ page }) => {
  await page.goto("/stock");
  await page.keyboard.press("a");
  await expect(page).toHaveURL(/\/items\/new$/);
  await page.goto("/");
  await page.keyboard.press("l");
  await expect(page).toHaveURL(/\/services\/new$/);

  // Typed in a field, they are just letters.
  await page.goto("/items/new");
  await page.getByLabel("Name", { exact: true }).pressSequentially("al");
  await expect(page).toHaveURL(/\/items\/new$/);

  // With unsaved changes, leaving asks first; Cancel stays.
  await page.locator("main h1").click();
  page.once("dialog", (d) => d.dismiss());
  await page.keyboard.press("l");
  await expect(page).toHaveURL(/\/items\/new$/);
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue("al");
});

// Opens the Appearance panel (from the sidebar; on a phone, the More sheet)
// unless it is open.
async function appearance(page: Page) {
  const panel = page.locator("#appearance");
  if (!(await panel.evaluate((e) => e.matches(":popover-open")))) {
    await (await sidebar(page)).getByRole("button", { name: "Appearance" }).click();
  }
  await expect(panel).toBeVisible();
  return panel;
}

// Picks a theme in Appearance and waits until it is saved.
async function pickTheme(page: Page, name: "System" | "Light" | "Dark") {
  const saved = page.waitForResponse((r) => r.url().endsWith("/theme") && r.request().method() === "POST");
  await (await appearance(page)).getByRole("button", { name, exact: true }).click();
  expect((await saved).status()).toBe(204);
}

test("pick the theme in Appearance", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/");
  const html = page.locator("html");
  const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const light = await bg();
  const pressed = async () => (await appearance(page)).locator('.theme-pick button[aria-pressed="true"]');

  await expect(await pressed()).toHaveText("System");
  // At once, without a reload, and still after one.
  await page.evaluate(() => { (window as any).same = true; });
  await pickTheme(page, "Dark");
  await expect(html).toHaveAttribute("data-theme", "dark");
  expect(await bg()).not.toBe(light);
  expect(await page.evaluate(() => (window as any).same)).toBe(true);
  await expect(await pressed()).toHaveText("Dark");
  await page.reload();
  await expect(html).toHaveAttribute("data-theme", "dark");
  await expect(await pressed()).toHaveText("Dark");

  // Back to what the system shows: the choice is forgotten.
  await pickTheme(page, "System");
  await expect(html).not.toHaveAttribute("data-theme");
  expect(await bg()).toBe(light);
  await page.reload();
  await expect(html).not.toHaveAttribute("data-theme");
  await expect(await pressed()).toHaveText("System");
});

test("pick the accent in Appearance", async ({ page }) => {
  await page.goto("/");
  const html = page.locator("html");
  const accent = () => page.evaluate(() => {
    const probe = document.createElement("div");
    probe.style.color = "var(--accent)";
    document.body.append(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  });
  const indigo = await accent();
  const saved = () => page.waitForResponse((r) => r.url().endsWith("/accent") && r.request().method() === "POST");
  const panel = await appearance(page);
  await expect(panel.getByRole("button", { name: "Indigo", exact: true })).toHaveAttribute("aria-pressed", "true");

  // A preset recolours the page at once and is remembered on this device.
  let done = saved();
  await panel.getByRole("button", { name: "Ocean", exact: true }).click();
  await done;
  await expect(html).toHaveAttribute("style", /--accent-h: 228; --accent-i: 50/);
  expect(await accent()).not.toBe(indigo);
  await expect(panel.getByRole("button", { name: "Ocean", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(panel.getByRole("slider", { name: "Colour" })).toHaveValue("228");
  await page.reload();
  await expect(html).toHaveAttribute("style", /--accent-h: 228; --accent-i: 50/);

  // The sliders: any hue and intensity, saved once let go.
  done = saved();
  await (await appearance(page)).getByRole("slider", { name: "Colour" }).fill("150");
  await done;
  await page.reload();
  await expect(html).toHaveAttribute("style", /--accent-h: 150; --accent-i: 50/);
  await expect((await appearance(page)).locator('.swatch[aria-pressed="true"]')).toHaveCount(0);

  // Back to Indigo forgets it.
  done = saved();
  await (await appearance(page)).getByRole("button", { name: "Back to Indigo" }).click();
  await done;
  expect(await accent()).toBe(indigo);
  await page.reload();
  await expect(html).not.toHaveAttribute("style");
  expect((await page.context().cookies()).find((c) => c.name === "accent")).toBeUndefined();
});

test("Back shows the theme chosen on the next page", async ({ page }, testInfo) => {
  // Back shows the page as it was left, from the page cache or the HTTP
  // cache, with the theme it had then; it must switch to the one chosen.
  const check = async (p: Page, fromPageCache: boolean) => {
    await p.emulateMedia({ colorScheme: "light" });
    await p.goto("/stock");
    await p.evaluate(() => {
      window.addEventListener("pageshow", (e) => { (window as any).fromCache = e.persisted; });
    });
    await p.goto("/");
    await pickTheme(p, "Dark");
    await expect(p.locator("html")).toHaveAttribute("data-theme", "dark");
    await p.goBack({ waitUntil: "commit" });
    await expect(p).toHaveURL(/\/stock$/);
    if (fromPageCache) expect(await p.evaluate(() => (window as any).fromCache)).toBe(true);
    await expect(p.locator("html")).toHaveAttribute("data-theme", "dark");
    await expect(p.locator('meta[name="color-scheme"]')).toHaveAttribute("content", "dark");

    // And back to following the system.
    await p.goForward({ waitUntil: "commit" });
    await pickTheme(p, "System");
    await expect(p.locator("html")).not.toHaveAttribute("data-theme");
    await p.goBack({ waitUntil: "commit" });
    await expect(p.locator("html")).not.toHaveAttribute("data-theme");
  };
  await check(page, false);

  // Playwright turns Chromium's page cache off, and its headless shell
  // has none; this full Chromium has it on.
  const browser = await chromium.launch({ channel: "chromium", ignoreDefaultArgs: ["--disable-back-forward-cache"] });
  try {
    const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
    await check(await context.newPage(), true);
  } finally {
    await browser.close();
  }
});

// Waits for an element's transitions (a sheet sliding in) to end.
async function settled(l: Locator) {
  await l.evaluate((e) => Promise.all(e.getAnimations().map((a) => a.finished)));
}

// The sidebar, for its buttons: on a phone, the sheet the tab bar's More
// opens (opened if it isn't).
async function sidebar(page: Page) {
  const sheet = page.locator("#more");
  const more = page.locator(".tabbar").getByRole("button", { name: "More" });
  if ((await more.isVisible()) && !(await sheet.evaluate((e) => e.matches(":popover-open")))) await more.click();
  return sheet;
}

// Taps a vehicle in the sidebar's Work on chips (on a phone, in the More
// sheet).
async function workOn(page: Page, name: string) {
  await page.goto("/");
  await (await sidebar(page)).locator(".work-on").getByRole("button", { name }).click();
}

// The vehicle worked on, as the working_on cookie wants it, for tests that
// look at pages while working on one.
async function workingCookie(page: Page, baseURL: string, name: string) {
  // The list is out of view while another vehicle is worked on.
  await page.context().clearCookies({ name: "working_on" });
  await page.goto("/vehicles");
  const href = (await page.locator("a.vehicle-card").filter({ hasText: name }).first().getAttribute("href")) as string;
  await page.context().addCookies([{ name: "working_on", value: href.split("/").pop() as string, url: baseURL }]);
}

test("work on a vehicle", async ({ page }, testInfo) => {
  await workOn(page, "KTM 390");
  const bar = page.locator(".working-on");
  await expect(bar).toContainText("Working on KTM 390");
  if (phone(testInfo)) {
    // Edge to edge at the top, a bottom border only (its rules once lost to
    // the desktop ones that come later in app.css).
    const box = await bar.boundingBox();
    const width = await page.evaluate(() => document.documentElement.clientWidth);
    expect(box && box.x === 0 && box.y === 0 && box.width === width, `bar at ${JSON.stringify(box)} in ${width}px`).toBe(true);
    await expect(bar).toHaveCSS("border-top-width", "0px");
    await expect(bar).toHaveCSS("border-bottom-width", "1px");
    // One row: the buttons in 5px padding, and the bottom border.
    const buttons = await bar.locator(".working-actions").boundingBox();
    expect(box?.height, "the bar wraps onto a second row").toBe((buttons?.height ?? 0) + 11);
  }
  await expect(page).toHaveURL(/\/\?v=\d+$/);
  // The bar names the vehicle; the title doesn't say it again.
  await expect(page.locator("main h1")).toHaveText("Dashboard");

  // The stock tab goes to the vehicle's stock, and says so in its address.
  await page.goto("/stock");
  await expect(page).toHaveURL(/\/stock\?general=1&v=\d+$/);
  await expect(page.locator("main h1")).toHaveText("Stock");
  await expect(page.locator("#results")).not.toContainText("Hiflofiltro");
  // The page's vehicle chips are hidden: the bar is the vehicle control.
  await expect(page.locator("#filter-all")).toHaveCount(0);
  // A search says what it finds in other vehicles.
  await page.getByPlaceholder(/Search name/).fill("Oil Filter");
  await expect(page.locator(".results-head")).toContainText("1 more in other vehicles");
  await expect(page).toHaveURL(/q=Oil/);
  await expect(page.locator("#results")).not.toContainText("Hiflofiltro");

  // Add item starts with the vehicle; Log service doesn't ask for one.
  await page.locator("main h1").click();
  await page.keyboard.press("a");
  await expect(page).toHaveURL(/\/items\/new$/);
  await expect(page.locator("label.chip-toggle").filter({ hasText: exact("KTM 390") }).locator("input")).toBeChecked();
  await expect(page.locator("label.chip-toggle").filter({ hasText: exact("KTM 1290") }).locator("input")).not.toBeChecked();
  await page.goto("/services/new");
  await expect(page.locator("#vehicle_id")).toHaveCount(0);

  // Switch, then Back: the page from before the switch shows the bar, and
  // its vehicle, as they are now. A phone switches
  // in the bar (it lists the other vehicles only); a desktop with the
  // sidebar's Work on chips, so its bar has no Switch.
  await page.goto("/stock");
  await expect(page).toHaveURL(/v=\d+/);
  const k390 = new URL(page.url()).searchParams.get("v");
  if (phone(testInfo)) {
    await bar.getByRole("button", { name: "Switch" }).click();
    await expect(page.locator("#switch").getByRole("button", { name: "KTM 390" })).toHaveCount(0);
    await page.locator("#switch").getByRole("button", { name: "KTM 1290" }).click();
  } else {
    await expect(bar.getByRole("button", { name: "Switch" })).toBeHidden();
    await page.locator(".sidebar .work-on").getByRole("button", { name: "KTM 1290" }).click();
  }
  await expect(bar).toContainText("Working on KTM 1290");
  await expect(page.locator("main h1")).toHaveText("Stock");
  // Back brings the KTM 390's stock from before the switch: it reloads, and
  // with the bar on the KTM 1290 it shows the KTM 1290's.
  await page.goBack();
  await expect(bar).toContainText("Working on KTM 1290");
  await expect(page).not.toHaveURL(new RegExp(`v=${k390}(&|$)`));
  await expect(page.locator("main h1")).toHaveText("Stock");
  await expect(page.locator("#results")).toContainText("Hiflofiltro");

  // Shopping is the vehicle's part of the list, with no chips of its own,
  // and the Shop count is that part's.
  const shopCount = async () => {
    const badge = page.locator('.nav a[href="/shopping"] .count:visible, .tabbar a[href="/shopping"] .count:visible').first();
    return (await badge.count()) ? Number(await badge.innerText()) : 0;
  };
  await page.goto("/shopping");
  await expect(page).toHaveURL(/\/shopping\?v=\d+$/);
  await expect(page.locator("main h1")).toHaveText("Shopping");
  await expect(page.locator('[aria-label="Filter by vehicle"]')).toHaveCount(0);
  const entries = Number((await page.locator(".card-head").filter({ hasText: "On the list" }).locator(".meta").innerText()).split(" ")[0]);
  expect(await shopCount()).toBe(entries);

  // All vehicles stops and keeps the search: every vehicle's matches.
  await page.goto("/stock");
  await page.getByPlaceholder(/Search name/).fill("Oil Filter");
  await expect(page).toHaveURL(/q=Oil/);
  await bar.getByRole("button", { name: "All vehicles" }).click();
  await expect(page.locator(".working-on")).toHaveCount(0);
  await expect(page).toHaveURL(/\/stock\?q=Oil\+Filter$/);
  await expect(page.locator("#results")).toContainText("Hiflofiltro");
  await expect(page.locator("#filter-all")).toBeVisible();
  // Without one, Shopping is the whole list, and its chips filter it.
  await page.goto("/shopping");
  await expect(page).toHaveURL(/\/shopping$/);
  const filter = page.locator('[aria-label="Filter by vehicle"]');
  await filter.getByRole("link", { name: "KTM 1290" }).click();
  await expect(page).toHaveURL(/\/shopping\?v=\d+$/);
  await expect(page.locator("main h1")).toHaveText("KTM 1290 shopping");
  await filter.getByRole("link", { name: "All vehicles" }).click();
  await expect(page).toHaveURL(/\/shopping$/);
});

test("Back after switching vehicles shows the one worked on now", async ({ page }, testInfo) => {
  // Back may bring a page from before the switch out of the page cache,
  // which the server never sees: app.js compares the cookie with the page
  // and reloads it. Trash isn't narrowed, so only the bar tells.
  const base = testInfo.project.use.baseURL as string;
  const check = async (p: Page, fromPageCache: boolean) => {
    await p.addInitScript(() => {
      window.addEventListener("pageshow", (e) => { if (e.persisted) sessionStorage.setItem("restored", "1"); });
    });
    await workingCookie(p, base, "KTM 390");
    await p.goto("/trash");
    await expect(p.locator(".working-on")).toContainText("Working on KTM 390");
    await workingCookie(p, base, "KTM 1290"); // another page, then the switch
    await p.goBack();
    await expect(p).toHaveURL(/\/trash$/);
    await expect(p.locator(".working-on")).toContainText("Working on KTM 1290");
    if (fromPageCache) expect(await p.evaluate(() => sessionStorage.getItem("restored"))).toBe("1");
  };
  await check(page, false);

  // Playwright turns Chromium's page cache off; this full Chromium has it.
  const browser = await chromium.launch({ channel: "chromium", ignoreDefaultArgs: ["--disable-back-forward-cache"] });
  try {
    const context = await browser.newContext({ baseURL: base });
    await check(await context.newPage(), true);
  } finally {
    await browser.close();
  }
});

test("the nav's Vehicle opens the vehicle worked on, and a phone's More holds the sidebar", async ({ page }, testInfo) => {
  await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
  await page.goto("/shopping");
  const sheet = page.locator("#more");
  const mobile = phone(testInfo);
  if (mobile) {
    await expect(sheet).toBeHidden();
    await sidebar(page);
    await expect(sheet).toBeVisible();
    // It holds what the tab bar hasn't: Activity, the theme, Work on.
    await expect(sheet.getByRole("link", { name: "Activity" })).toBeVisible();
    await expect(sheet.getByRole("link", { name: "Stock", exact: true })).toBeHidden();
    await expect(sheet.locator(".work-on")).toBeVisible();
    // A tap outside closes it.
    await page.mouse.click(10, 10);
    await expect(sheet).toBeHidden();
  }
  await (await sidebar(page)).getByRole("link", { name: "Vehicle", exact: true }).click();
  await expect(page).toHaveURL(/\/vehicles\/\d+$/);
  await expect(page.locator("main h1")).toHaveText("KTM 390");
  if (mobile) {
    await expect(sheet).toBeHidden();
    await expect(page.locator(".tabbar .current")).toHaveText("More");
  }
  // On its own page the bar's name is no link.
  await expect(page.locator(".working-on a.working-name")).toHaveCount(0);
  // The list and the other vehicles are out of view meanwhile: no way to
  // them from its page, and an old link opens the vehicle worked on.
  await expect(page.locator(".page-head").getByRole("link", { name: "Vehicles", exact: true })).toHaveCount(0);
  await page.goto("/vehicles");
  await expect(page).toHaveURL(/\/vehicles\/\d+$/);
  await expect(page.locator("main h1")).toHaveText("KTM 390");
  // The bar's name opens the vehicle too.
  await page.goto("/shopping");
  await page.locator(".working-on a.working-name").click();
  await expect(page.locator("main h1")).toHaveText("KTM 390");
  // Switching on its page opens the one now worked on.
  if (mobile) {
    await page.locator(".working-on").getByRole("button", { name: "Switch" }).click();
    await page.locator("#switch").getByRole("button", { name: "KTM 1290" }).click();
  } else {
    await page.locator(".sidebar .work-on").getByRole("button", { name: "KTM 1290" }).click();
  }
  await expect(page).toHaveURL(/\/vehicles\/\d+$/);
  await expect(page.locator("main h1")).toHaveText("KTM 1290");
  // The dashboard leaves out its Vehicles panel meanwhile.
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Vehicles", exact: true })).toHaveCount(0);
  // Stopping on a vehicle's page lists them all, with the way back.
  await page.goto("/vehicles");
  await page.locator(".working-on").getByRole("button", { name: "All vehicles" }).click();
  await expect(page).toHaveURL(/\/vehicles$/);
  await expect(page.locator(".working-on")).toHaveCount(0);
  await expect(page.locator("a.vehicle-card").filter({ hasText: "KTM 390" })).toBeVisible();
});

test("a phone's More and Switch grey the page, not what opened them", async ({ page }, testInfo) => {
  test.skip(!phone(testInfo), "More and Switch are a phone's");
  await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
  await page.goto("/shopping");
  // Taps in the middle of what a selector finds. The grey (the popover's
  // ::backdrop) takes a tap and only closes it; what isn't grey works.
  const tap = async (selector: string) => {
    const b = (await page.locator(selector).first().boundingBox())!;
    await page.mouse.click(b.x + b.width / 2, b.y + b.height / 2);
  };
  const more = page.locator(".tabbar").getByRole("button", { name: "More" });
  const sheet = page.locator("#more");
  const bar = page.locator(".working-on");
  const tray = page.locator("#switch");
  const stockTab = page.locator(".tabbar a[href='/stock']");

  // More: the bar under the grey only closes it.
  await more.click();
  await expect(sheet).toBeVisible();
  // It floats just above the tab bar, as wide, rounded on every corner
  // (design.md#6-layout); measured once it has slid into place.
  await settled(sheet);
  const sh = (await sheet.boundingBox())!;
  const tabs = (await page.locator(".tabbar").boundingBox())!;
  expect(Math.round(tabs.y - (sh.y + sh.height)), "the sheet's gap above the tab bar").toBe(8);
  expect(sh.x === tabs.x && sh.width === tabs.width, `sheet at ${JSON.stringify(sh)}, tab bar ${JSON.stringify(tabs)}`).toBe(true);
  await expect(sheet).toHaveCSS("border-radius", "14px");
  // The build isn't in it: it is the logo's title (and /healthz).
  await expect(sheet.locator(".sidebar-foot")).toBeHidden();
  // More is lit while it is open, as the current tab (Shop) is.
  await expect(more).toHaveCSS("color", await page.locator(".tabbar a[href='/shopping']").evaluate((e) => getComputedStyle(e).color));
  await tap(".working-on a.working-name");
  await expect(sheet).toBeHidden();
  await expect(page).toHaveURL(/\/shopping\?v=\d+$/);
  // The tab bar isn't grey: More again closes it, another tab opens.
  await more.click();
  await expect(sheet).toBeVisible();
  await more.click();
  await expect(sheet).toBeHidden();
  await more.click();
  await tap(".tabbar a[href='/stock']");
  await expect(page).toHaveURL(/\/stock\?/);
  await page.goto("/shopping");
  await expect(stockTab).toBeVisible();

  // Switch: the tray hangs under the bar, as wide, rounded at the bottom,
  // over the bar's bottom border, so the two join without a seam.
  await bar.getByRole("button", { name: "Switch" }).click();
  await expect(tray).toBeVisible();
  await settled(tray);
  const b = (await bar.boundingBox())!;
  const t = (await tray.boundingBox())!;
  expect(t.y, "the tray's top").toBe(b.y + b.height - 1);
  expect(t.x === 0 && t.width === b.width, `tray at ${JSON.stringify(t)}, bar ${JSON.stringify(b)}`).toBe(true);
  await expect(tray).toHaveCSS("border-radius", "0px 0px 14px 14px");
  // Everything below is grey, the tab bar too: a tap only closes it.
  await tap(".tabbar a[href='/stock']");
  await expect(tray).toBeHidden();
  await expect(page).toHaveURL(/\/shopping\?v=\d+$/);
  // Esc closes it too.
  await bar.getByRole("button", { name: "Switch" }).click();
  await expect(tray).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(tray).toBeHidden();
  // The bar isn't grey: its name opens the vehicle.
  await bar.getByRole("button", { name: "Switch" }).click();
  await tap(".working-on a.working-name");
  await expect(page.locator("main h1")).toHaveText("KTM 390");
  await expect(tray).toBeHidden();
});

test("Ctrl-click or middle-click on Copy as text opens the text", async ({ page, context }) => {
  await page.goto("/stock");
  const copy = page.getByRole("link", { name: "Copy as text" });
  // app.js must leave a modified or middle click to the browser, which then
  // opens the link like any other. Opening the tab is the browser's part:
  // waiting for it from a real Ctrl-click timed out now and then in
  // headless Chromium on CI. So: does app.js take the click over (a window
  // listener runs after the document's, sees, and stops the navigation)?
  const takenOver = (init: MouseEventInit) => copy.evaluate((a, init) => {
    let seen: boolean | undefined;
    window.addEventListener("click", (e) => { seen = e.defaultPrevented; e.preventDefault(); }, { once: true });
    a.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, ...init }));
    return seen;
  }, init);
  for (const init of [{ ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { altKey: true }, { button: 1 }]) {
    expect(await takenOver(init), JSON.stringify(init)).toBe(false);
  }
  await expect(page).toHaveURL(/\/stock$/);
  await expect(copy).not.toHaveAttribute("data-copied");

  // What the opened tab shows: the link's own URL, the text.
  const tab = await context.newPage();
  await tab.goto((await copy.getAttribute("href"))!);
  expect(tab.url()).toContain("/stock/export.txt");
  await expect(tab.locator("body")).toContainText("Silkolene 15W50 PRO 4 XP");
  await tab.close();

  // A plain click is app.js's to copy. (This one, made by a script, may not
  // write the clipboard, and then the page shows the text: so it comes last.)
  expect(await takenOver({})).toBe(true);
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
    await expect(page.locator(".flash-success")).toContainText(i === 0 ? "Saved the photo" : "Replaced the photo");
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

test("a large photo is shrunk in the browser, and the server shrinks an original", async ({ page }) => {
  const big = gradientPNG(2400, 1800);
  await page.goto("/stock");
  await page.locator(".item-row a.item-name").filter({ hasText: "Hiflofiltro" }).click();
  const item = new URL(page.url()).pathname;
  const sent: Buffer[] = [];
  await page.route("**/items/*/photo", (route) => {
    if (route.request().method() === "POST") sent.push(uploadedPhoto(route.request()));
    return route.continue();
  });
  await page.evaluate(() => { (window as any).before = true; });
  await page.locator('input[type="file"][name="photo"]').setInputFiles({ name: "big.png", mimeType: "image/png", buffer: big });
  await page.waitForFunction(() => !(window as any).before);
  await expect(page.locator(".flash-success")).toContainText("Replaced the photo");
  expect(sent).toHaveLength(1);
  expect(imageSize(sent[0])).toEqual({ type: "jpeg", w: 1600, h: 1200 });
  expect(sent[0].length).toBeLessThan(big.length);

  // The server never relies on the browser: sent as it is (when the browser
  // can't decode it, say), the PNG is shrunk there.
  const res = await page.request.post(item + "/photo", { multipart: { photo: { name: "big.png", mimeType: "image/png", buffer: big } } });
  expect(res.ok()).toBe(true);
  const photo = await page.request.get(item + "/photo");
  expect(imageSize(await photo.body())).toEqual({ type: "jpeg", w: 1600, h: 1200 });
});

test("the photo opens larger on the page, and Close, Esc or a tap outside closes it", async ({ page, context }) => {
  await page.goto("/stock");
  await page.locator(".item-row a.item-name").filter({ hasText: "Hiflofiltro" }).click();
  const item = page.url();
  const open = page.getByRole("button", { name: "Show the photo larger" });
  const viewer = page.locator("dialog#photo-view");
  await expect(viewer).toBeHidden();

  // Close: the photo fills the window (within the page's margin), focus is back on the photo.
  await open.click();
  await expect(viewer).toBeVisible();
  await expect(viewer.getByRole("img")).toHaveJSProperty("complete", true);
  const shown = (await viewer.boundingBox())!;
  const win = page.viewportSize()!;
  expect(Math.max(shown.width / win.width, shown.height / win.height)).toBeGreaterThan(.8);
  expect(shown.width).toBeLessThanOrEqual(win.width);
  expect(shown.height).toBeLessThanOrEqual(win.height);
  await viewer.getByRole("button", { name: "Close" }).click();
  await expect(viewer).toBeHidden();
  await expect(open).toBeFocused();

  // Esc.
  await open.click();
  await expect(viewer).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(viewer).toBeHidden();

  // A tap outside it (the photo is 4:3, so the top-left corner is outside on any of the projects).
  await open.click();
  await expect(viewer).toBeVisible();
  await page.mouse.click(2, 2);
  await expect(viewer).toBeHidden();

  // The page behind doesn't scroll, over the backdrop or over the photo
  // (made tall enough to scroll on every screen); closed, it does.
  await page.evaluate(() => { document.body.style.minHeight = "300vh"; });
  await open.click();
  await expect(viewer).toBeVisible();
  const before = await page.evaluate(() => window.scrollY);
  const box = (await viewer.boundingBox())!;
  for (const [x, y] of [[2, 2], [box.x + box.width / 2, box.y + box.height / 2]]) {
    await page.mouse.move(x, y);
    await page.mouse.wheel(0, 600);
  }
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => window.scrollY)).toBe(before);
  await page.keyboard.press("Escape");
  await expect(viewer).toBeHidden();
  await page.mouse.wheel(0, 600);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(before);

  // All on the same page: no new tab, no navigation.
  expect(context.pages()).toHaveLength(1);
  expect(page.url()).toBe(item);
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
  const overflows = async (p: string) => {
    await page.goto(p);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, `horizontal overflow on ${p}`).toBeLessThanOrEqual(0);
  };
  for (const p of paths) await overflows(p);
  // The bar while working on a vehicle, with its Switch open.
  await workingCookie(page, test.info().project.use.baseURL as string, "KTM 1290");
  for (const p of ["/", "/stock", "/services", "/activity", "/shopping"]) await overflows(p);
  // Switch is a phone's: a desktop switches in the sidebar.
  const switchButton = page.locator(".working-on").getByRole("button", { name: "Switch" });
  if (!(await switchButton.isVisible())) return;
  await switchButton.click();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, "horizontal overflow with Switch open").toBeLessThanOrEqual(0);
  // Nor past the left edge, which scrollWidth doesn't count: the list
  // once hung from the Switch button and stuck out on a phone.
  const pick = await page.locator(".working-pick").boundingBox();
  const width = await page.evaluate(() => document.documentElement.clientWidth);
  expect(pick && pick.x >= 0 && pick.x + pick.width <= width, `Switch list at ${JSON.stringify(pick)} in ${width}px`).toBe(true);
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
  await expect(th("Item")).not.toHaveAttribute("aria-sort");

  await th("Item").getByRole("link").click();
  await expect(page).toHaveURL(/\/activity\?sort=item$/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "ascending");
  await expect(th("Date")).not.toHaveAttribute("aria-sort");
  const az = await columnRuns(page, 2);
  expect(az.length).toBeGreaterThan(2);

  await th("Item").getByRole("link").click();
  await expect(page).toHaveURL(/\/activity\?dir=desc&sort=item$/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "descending");
  expect(await columnRuns(page, 2)).toEqual([...az].reverse());
  await expect(th("Item").getByRole("link")).toHaveAttribute("title", "Sort by Item, ascending");

  // A kind chip keeps the sort.
  await page.locator(".chips").getByRole("link", { name: "Bought" }).click();
  await expect(page).toHaveURL(/dir=desc&kind=bought&sort=item/);
  await expect(th("Item")).toHaveAttribute("aria-sort", "descending");

  // Keyboard: the headers are links, with a visible focus ring.
  await th("Price").getByRole("link").focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/kind=bought&sort=price/);
  await expect(th("Price")).toHaveAttribute("aria-sort", "descending");
  const cells = await page.locator("table.table tbody tr td:nth-child(5)").allInnerTexts();
  const prices = cells.filter((c) => c.trim()).map((p) => parseFloat(p.replace(/[^\d.]/g, "")));
  expect(prices.length).toBeGreaterThan(1);
  expect(prices).toEqual([...prices].sort((a, b) => b - a));
  // Purchases without a price come last.
  expect(cells.slice(prices.length).every((c) => !c.trim())).toBe(true);
});

test("pick a year on the Activity page", async ({ page }) => {
  await page.goto("/activity?kind=bought");
  const year = page.getByLabel("Year");
  const thisYear = await year.locator('option:not([value=""])').first().getAttribute("value");
  expect(thisYear).toBeTruthy();
  await year.selectOption(thisYear!);
  // The page reloads for that year and keeps the kind.
  await expect(page).toHaveURL(new RegExp(`\\?kind=bought&year=${thisYear}$`));
  await expect(year).toHaveValue(thisYear!);
  await expect(page.getByText(`Every change to your stock in ${thisYear}`)).toBeVisible();
  await expect(page.locator("table.table tbody tr").first()).toBeVisible();
  await year.selectOption({ label: "All years" });
  await expect(page).toHaveURL(/\?kind=bought&year=$/);
  await expect(year).toHaveValue("");
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
  await expect(page.locator("#results")).not.toContainText("Clear filters");
  await page.locator("#results").getByRole("link", { name: "Grouped" }).click();
  await expect(page).toHaveURL(/\/stock\?dir=asc&sort=recent$/);
  await expect(page.getByLabel("Sort by")).toHaveValue("recent");
});

// Opt-in: SCREENSHOT_DIR=/tmp/shots npx playwright test (see docs/development.md)
// Every page and form, in both themes: <dir>/<desktop|mobile>/<light|dark>/<name>.png.
// The desktop shots come from "laptop" (1360px): a 2K page is too wide to read
// in the README.
test("screenshots", async ({ page }, testInfo) => {
  const dir = process.env.SCREENSHOT_DIR;
  test.skip(!dir, "set SCREENSHOT_DIR to capture screenshots");
  test.skip(!["laptop", "mobile"].includes(testInfo.project.name), "the docs' screenshots are the laptop's and the phone's");
  const device = phone(testInfo) ? "mobile" : "desktop";
  test.setTimeout(180_000);
  // The first link on a page that matches, for the pages of one record.
  const link = async (url: string, selector: string) => {
    await page.goto(url);
    return (await page.locator(selector).first().getAttribute("href", { timeout: 5_000 })) as string;
  };
  const item = await link("/stock?q=Silkolene", ".item-row a.item-name");
  const vehicle = await link("/vehicles", "a.vehicle-card");
  const kit = await link("/services", "a.kit-row");
  const service = await link("/services", 'a.title[href^="/services/"]');
  const movement = await link("/activity", 'a[href^="/movements/"]');
  // The earlier tests leave the shopping list empty: put something on it.
  await page.goto("/shopping");
  await page.getByLabel("Something new").fill("Chain wax");
  await page.getByLabel("Quantity").fill("1");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.locator('a[href$="/bought"]')).toHaveCount(1);
  const bought = await link("/shopping", 'a[href$="/bought"]');
  const shots: [string, string][] = [
    ["dashboard", "/"],
    ["stock", "/stock"],
    ["stock-list", "/stock?view=list"],
    ["stock-low", "/stock?low=1"],
    ["stock-archived", "/stock?archived=1"],
    ["stock-sorted", "/stock?sort=expiry&dir=desc"],
    ["item", item],
    ["item-new", "/items/new"],
    ["item-edit", item + "/edit"],
    ["vehicles", "/vehicles"],
    ["vehicles-archived", "/vehicles?archived=1"],
    ["vehicle", vehicle],
    ["vehicle-new", "/vehicles/new"],
    ["vehicle-edit", vehicle + "/edit"],
    ["services", "/services"],
    ["services-archived", "/services?archived=1"],
    ["service", service],
    ["service-new", "/services/new"],
    ["service-edit", service + "/edit"],
    ["kit", kit],
    ["kit-sorted", kit + "?sort=missing"],
    ["kit-new", "/kits/new"],
    ["kit-edit", kit + "/edit"],
    ["shopping", "/shopping"],
    ["shopping-bought", bought],
    ["activity", "/activity"],
    ["activity-sorted", "/activity?sort=price"],
    ["movement-edit", movement],
    ["trash", "/trash"],
    ["not-found", "/no-such-page"],
  ];
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const [name, url] of shots) {
      await page.goto(url);
      // A full-page shot keeps the fixed tab bar where the first screen ends,
      // over the content: put it at the end of the page, as when scrolled down.
      // The sticky sidebar is one screen high too: stretch it down the page,
      // so its footer is at the bottom and not halfway down.
      await page.addStyleTag({
        content: "body { position: relative; } .tabbar { position: absolute !important; } .sidebar { position: static; height: auto; }",
      });
      await page.screenshot({ path: `${dir}/${device}/${scheme}/${name}.png`, fullPage: true });
    }
  }
  // Working on a vehicle: its dashboard and its stock, with the bar.
  await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const [name, url] of [["dashboard-working", "/"], ["stock-working", "/stock"]]) {
      await page.goto(url);
      await page.addStyleTag({
        content: "body { position: relative; } .tabbar { position: absolute !important; } .sidebar { position: static; height: auto; }",
      });
      await page.screenshot({ path: `${dir}/${device}/${scheme}/${name}.png`, fullPage: true });
    }
  }
});

// Boxes that don't start and end on whole CSS pixels (docs/conventions.md):
// the browser snaps text to whole pixels but not icons or borders, so text
// on a fraction moves against its icon from row to row, and a border on one
// is drawn blurred across two. Icons, images and inline text are skipped,
// and so is what is hidden from sight (.sr-only, or clipped away like it,
// as the Working on bar's button names).
async function offGrid(page: Page) {
  return page.evaluate(() => {
    const clipped = (el: Element) => { const cs = getComputedStyle(el); return cs.clip === "rect(0px, 0px, 0px, 0px)" || cs.clipPath === "inset(50%)"; };
    const frac = (v: number) => Math.abs(v - Math.round(v)) > 0.01;
    const out: string[] = [];
    for (const el of document.querySelectorAll("body *")) {
      if (el.closest("svg") || el.closest(".sr-only") || clipped(el) || ["IMG", "SCRIPT", "STYLE", "NOSCRIPT", "TEMPLATE", "OPTION", "BR"].includes(el.tagName)) continue;
      const display = getComputedStyle(el).display;
      if (display === "inline" || display === "none" || display === "contents") continue;
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height) continue;
      const top = r.top + window.scrollY;
      if (frac(top) || frac(r.height)) {
        out.push(`${el.tagName.toLowerCase()}${[...el.classList].map((c) => "." + c).join("")} top ${top.toFixed(2)} height ${r.height.toFixed(2)}`);
      }
    }
    return out;
  });
}

// Text that doesn't line up: an icon against the capitals of the text
// beside it, and the capitals of things side by side in a centred row (a
// table row, a list row, a toolbar), on the same line and one line tall
// each, by more than 1px; the capitals against the middle of their own box
// (a button, chip, badge, nav link, table header or heading bar) by more
// than .3px, since past that the space above them is a whole pixel more
// than below (Chrome puts the baseline on a whole pixel, so only some font
// sizes can be centred: docs/design.md#text-centred-in-its-box). Chips and
// badges are 24px tall, chips you pick or tap 30px. The capitals' middle is
// the baseline (the text box's top plus Manrope's ascent, 1.066em, rounded)
// less half their height (.735em). Monospace text (SKUs) is left out.
async function misaligned(page: Page) {
  return page.evaluate(() => {
    const vis = (el: Element) => {
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && cs.visibility !== "hidden" && cs.opacity !== "0" && !el.closest(".sr-only") && cs.clip !== "rect(0px, 0px, 0px, 0px)" && cs.clipPath !== "inset(50%)";
    };
    const mono = (el: Element) => /mono|Menlo|Consolas/i.test(getComputedStyle(el).fontFamily);
    const caps = (t: Text) => {
      const fs = parseFloat(getComputedStyle(t.parentElement!).fontSize);
      const rg = document.createRange();
      rg.selectNodeContents(t);
      const r = rg.getClientRects()[0];
      return r.top + Math.round(1.066 * fs) - (0.735 * fs) / 2;
    };
    // An element's own text: direct, or inside its inline children.
    const ownText = (el: Element) => {
      const out: Text[] = [];
      const walk = (n: Node) => {
        for (const c of n.childNodes) {
          if (c.nodeType === Node.TEXT_NODE) { if ((c as Text).data.trim()) out.push(c as Text); }
          else if (c instanceof HTMLElement && getComputedStyle(c).display === "inline" && !mono(c)) walk(c);
        }
      };
      walk(el);
      return out;
    };
    const lines = (el: Element) => {
      const t = ownText(el)[0];
      if (!t) return 0;
      const rg = document.createRange();
      rg.selectNodeContents(t);
      return new Set([...rg.getClientRects()].map((r) => Math.round(r.top))).size;
    };
    // How tall everything inside an element is, icons and text included.
    const extent = (k: Element) => {
      let top = Infinity, bottom = -Infinity;
      const add = (r: DOMRect) => { if (r.height) { top = Math.min(top, r.top); bottom = Math.max(bottom, r.bottom); } };
      for (const n of k.querySelectorAll("*")) if (!(n instanceof SVGElement) || n.matches("svg.icon")) add(n.getBoundingClientRect());
      const tw = document.createTreeWalker(k, NodeFilter.SHOW_TEXT);
      for (let x = tw.nextNode(); x; x = tw.nextNode()) {
        if (!(x as Text).data.trim()) continue;
        const rg = document.createRange();
        rg.selectNodeContents(x);
        for (const r of rg.getClientRects()) add(r);
      }
      return [top, bottom];
    };
    const name = (el: Element) => el.tagName.toLowerCase() + [...el.classList].map((c) => "." + c).join("");
    const out: string[] = [];
    for (const svg of document.querySelectorAll("svg.icon")) {
      const el = svg.parentElement!;
      const t = ownText(el)[0];
      if (!vis(svg) || !t || mono(el) || getComputedStyle(el).flexDirection === "column") continue;
      const r = svg.getBoundingClientRect(), d = r.top + r.height / 2 - caps(t);
      if (Math.abs(d) > 1) out.push(`icon in ${name(el)} ${d.toFixed(2)}px off its text`);
    }
    const middle = (el: Element) => {
      const cs = getComputedStyle(el), r = el.getBoundingClientRect();
      return (r.top + parseFloat(cs.borderTopWidth) + r.bottom - parseFloat(cs.borderBottomWidth)) / 2;
    };
    // A kit start's status stands on the button name's baseline instead,
    // on purpose (docs/design.md, Status & State Badges).
    for (const el of document.querySelectorAll(".btn, .chip, .badge:not(.kit-start .badge), .nav a, .toggle, .segmented :is(label, a), .kbd, .count, .flash, .sort-link, .tile .label, .table th")) {
      const t = ownText(el)[0];
      if (!vis(el) || !t || mono(el) || lines(el) !== 1) continue;
      const d = caps(t) - middle(el);
      if (Math.abs(d) > 0.3) out.push(`text of ${name(el)} ${d.toFixed(2)}px off its middle`);
    }
    // A heading bar's heading, while the bar is one row.
    for (const bar of document.querySelectorAll(".card-head, .section-head")) {
      const h = bar.querySelector(":scope > h2");
      const t = h && ownText(h)[0];
      if (!h || !t || !vis(bar) || lines(h) !== 1) continue;
      const hr = h.getBoundingClientRect();
      if ([...bar.children].some((k) => vis(k) && k.getBoundingClientRect().top >= hr.bottom)) continue;
      const d = caps(t) - middle(bar);
      if (Math.abs(d) > 0.3) out.push(`heading of ${name(bar)} ${d.toFixed(2)}px off its middle`);
    }
    // Chips you pick or tap (the filter bars, the Switch tray) are a small
    // button's height; the sidebar's Work on and Stock by vehicle are list
    // rows, as tall as the nav's (design.md#10-chips-vs-badges).
    const tap = ".chip-toggle, .work-on .chip, .nav-vehicles .chip";
    for (const el of document.querySelectorAll(`.chip:not(${tap}), .badge:not(.kit-start .badge)`)) {
      const h = el.getBoundingClientRect().height;
      if (vis(el) && h !== 24) out.push(`${name(el)} ${h}px tall, not 24px`);
    }
    for (const el of document.querySelectorAll(`:is(${tap}):not(.sidebar .chip)`)) {
      const h = el.getBoundingClientRect().height;
      if (vis(el) && h !== 30) out.push(`${name(el)} ${h}px tall, not 30px`);
    }
    for (const el of document.querySelectorAll("body *")) {
      if (!vis(el) || el instanceof SVGElement) continue;
      const cs = getComputedStyle(el);
      const centred = cs.alignItems === "center" && ((cs.display.includes("flex") && !cs.flexDirection.startsWith("column")) || cs.display.includes("grid"));
      if (!centred && el.tagName !== "TR") continue;
      const kids = [...(el instanceof HTMLTableRowElement ? el.cells : el.children)].filter((k) => vis(k) && !(k instanceof SVGElement));
      const ms: [Element, number, number, number][] = [];
      for (const k of kids) {
        const t = ownText(k)[0];
        const kcs = getComputedStyle(k);
        // A kit's part rows are centred on purpose, name and stock meter as one
        // block, so the name sits above the amounts: those rows aren't compared.
        if (!t || mono(k) || lines(k) !== 1 || (el.tagName === "TR" && (kcs.verticalAlign !== "middle" || el.querySelector(".meter")))) continue;
        const [top, bottom] = extent(k);
        if (bottom - top <= 1.5 * parseFloat(kcs.lineHeight)) ms.push([k, caps(t), top, bottom]);
      }
      // Only what shares a line: a wrapped row is several.
      ms.sort((a, b) => a[2] - b[2]);
      const groups: { ms: typeof ms; bottom: number }[] = [];
      for (const m of ms) {
        const g = groups[groups.length - 1];
        if (g && m[2] < g.bottom) { g.ms.push(m); g.bottom = Math.max(g.bottom, m[3]); }
        else groups.push({ ms: [m], bottom: m[3] });
      }
      for (const g of groups) {
        const v = g.ms.map((m) => m[1]);
        const spread = Math.max(...v) - Math.min(...v);
        if (g.ms.length > 1 && spread > 1) out.push(`text in ${name(el)}: ${g.ms.map((m) => name(m[0])).join(", ")} ${spread.toFixed(2)}px apart`);
      }
    }
    return out;
  });
}

test("every box sits on whole pixels and its text lines up", async ({ page }) => {
  // Every page's offenders, so one run shows them all.
  const problems: string[] = [];
  const check = async (where: string) => {
    // Once what slides or fades in (a flash, a sheet) has come to rest.
    await page.evaluate(() => Promise.all(document.getAnimations().filter((a) => a.effect?.getTiming().iterations !== Infinity).map((a) => a.finished)));
    for (const box of await offGrid(page)) problems.push(`${where}: ${box}`);
    for (const text of await misaligned(page)) problems.push(`${where}: ${text}`);
  };
  const link = async (url: string, selector: string) => {
    await page.goto(url);
    return (await page.locator(selector).first().getAttribute("href", { timeout: 5_000 })) as string;
  };
  const item = await link("/stock?q=Silkolene", ".item-row a.item-name");
  const vehicle = await link("/vehicles", "a.vehicle-card");
  const kit = await link("/services", "a.kit-row");
  const service = await link("/services", 'a.title[href^="/services/"]');
  const movement = await link("/activity", 'a[href^="/movements/"]');
  // Something on the shopping list, and the flash that says so.
  await page.goto("/shopping");
  if ((await page.locator('a[href$="/bought"]').count()) === 0) {
    await page.getByLabel("Something new").fill("Chain wax");
    await page.getByLabel("Quantity").fill("1");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.locator(".flash")).toBeVisible();
    await check("/shopping with a flash");
  }
  const bought = await link("/shopping", 'a[href$="/bought"]');
  const paths = ["/", "/stock", "/stock?view=list", "/stock?low=1", "/stock?archived=1", "/stock?sort=expiry&dir=desc", item, "/items/new", item + "/edit",
    "/vehicles", "/vehicles?archived=1", vehicle, "/vehicles/new", vehicle + "/edit", "/services", "/services?archived=1", service, "/services/new", service + "/edit",
    kit, kit + "?sort=missing", "/kits/new", kit + "/edit", "/shopping", "/shopping?v=" + vehicle.split("/").pop(), bought, "/activity", "/activity?sort=price",
    "/activity?kind=bought&year=2026", movement, "/trash", "/no-such-page"];
  for (const p of paths) {
    await page.goto(p);
    await check(p);
  }
  // An open hint, and a form sent back with its errors.
  await page.goto("/items/new");
  await page.getByRole("button", { name: "More about Keep at least" }).click();
  await check("/items/new with a hint open");
  await page.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(page.locator(".error-summary")).toBeVisible();
  await check("/items/new with errors");
  // The kit form with its intermediate schedule expanded and its hint open.
  await page.goto(kit + "/edit");
  await page.getByLabel("Use an intermediate schedule").check();
  await page.getByRole("button", { name: "More about Intermediate months" }).click();
  await check(kit + "/edit with intermediate schedule and hint open");
  // Working on a vehicle: the bar on every page, and its Switch open.
  await workingCookie(page, test.info().project.use.baseURL as string, "KTM 390");
  for (const p of ["/", "/stock", "/stock?v=all", "/services", "/activity", "/shopping", item]) {
    await page.goto(p);
    await check(`${p} working on a vehicle`);
  }
  if (await page.locator(".working-on").getByRole("button", { name: "Switch" }).isVisible()) {
    await page.locator(".working-on").getByRole("button", { name: "Switch" }).click();
    await check("the Switch open");
    // A phone's More sheet, working on a vehicle and not.
    await page.goto("/shopping");
    await sidebar(page);
    await check("the More sheet open");
    await page.context().clearCookies({ name: "working_on" });
    await page.goto("/shopping");
    await sidebar(page);
    await check("the More sheet open, no vehicle worked on");
  }
  expect(problems).toEqual([]);
});

test("every page header has the same spacing", async ({ page }, testInfo) => {
  // Every page header is the same grid (design.md#7-page-headers): the
  // heading and the buttons share a row at least a button (36px) tall, the
  // heading centred in it, and the line under it (Archived, chips, badges,
  // then text) runs the full width, under the buttons too: 4px under the
  // row. In a main column under 680px (a phone, a narrow window) the heading
  // is at the top, the line 4px below, then the buttons. The item page once
  // centred its heading against its buttons, 3px lower than the rest, with
  // its badges 15px below.
  const link = async (url: string, selector: string) => {
    await page.goto(url);
    return (await page.locator(selector).first().getAttribute("href", { timeout: 5_000 })) as string;
  };
  const maybe = async (url: string, selector: string) => {
    await page.goto(url);
    const l = page.locator(selector).first();
    return (await l.count()) ? ((await l.getAttribute("href")) as string) : null;
  };
  const item = await link("/stock?q=Silkolene", ".item-row a.item-name");
  // Archived records only when earlier tests left some (the Go tests cover Archived leading the line).
  const archivedItem = await maybe("/stock?archived=1", ".item-row a.item-name");
  const vehicle = await link("/vehicles", "a.vehicle-card");
  const kit = await link("/services", "a.kit-row");
  const service = await link("/services", 'a.title[href^="/services/"]');
  const movement = await link("/activity", 'a[href^="/movements/"]');
  const purchase = await maybe("/activity?kind=bought", 'a[href^="/movements/"]');
  const archivedVehicle = await maybe("/vehicles?archived=1", "a.vehicle-card");
  const paths = ["/", "/stock", "/stock?archived=1", item, item.replace("/items/", "/activity?item="), "/items/new", item + "/edit",
    "/vehicles", "/vehicles?archived=1", vehicle, "/vehicles/new", vehicle + "/edit", "/services", "/services?archived=1", service, "/services/new", service + "/edit",
    kit, "/kits/new", kit + "/edit", "/shopping", "/activity", movement, "/trash", ...[purchase, archivedItem, archivedVehicle].filter((p): p is string => !!p)];
  const problems: string[] = [];
  // And the pages that show the Working on bar: the header starts below it.
  const working = ["/", "/stock", "/services", "/activity", "/shopping", item];
  const pages = [...paths, ...working.map((p) => "working " + p)];
  let cookie = false;
  for (const entry of pages) {
    const p = entry.replace(/^working /, "");
    if (entry !== p && !cookie) {
      await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
      cookie = true;
    }
    await page.goto(p);
    const m = await page.evaluate(() => {
      const main = document.querySelector(".main") as HTMLElement;
      const narrow = main.clientWidth - parseFloat(getComputedStyle(main).paddingLeft) - parseFloat(getComputedStyle(main).paddingRight) < 680;
      const bar = document.querySelector(".working-on");
      const contentTop = bar
        ? bar.getBoundingClientRect().bottom + parseFloat(getComputedStyle(bar).marginBottom)
        : main.getBoundingClientRect().top + parseFloat(getComputedStyle(main).paddingTop);
      const h1 = document.querySelector(".page-head h1");
      if (!h1) return null;
      const r = h1.getBoundingClientRect();
      const sub = h1.parentElement?.querySelector(":scope > .sub") ?? null;
      const actions = document.querySelector(".page-head .actions");
      // The line's order: Archived 0, chips 1, other badges 2, text 3.
      const rank = (el: Element) => {
        if (el.classList.contains("badge") && /^\s*Archived/.test(el.textContent ?? "")) return 0;
        if (el.matches(".chip, .chips")) return 1;
        if (el.matches(".badge")) return 2;
        return 3;
      };
      const ranks = sub && sub.matches(".flex") ? [...sub.children].map(rank) : [];
      return {
        narrow,
        // The heading row is a button tall: the heading's line (32px)
        // centred in 36px. A phone's smaller heading starts the column.
        want: narrow ? { top: 0, gap: 4 } : { top: (36 - Math.round(r.height)) / 2, gap: 4 + (36 - Math.round(r.height)) / 2 },
        inTitle: h1.parentElement?.classList.contains("page-title") ?? false,
        top: Math.round(r.top - contentTop),
        gap: sub ? Math.round(sub.getBoundingClientRect().top - r.bottom) : null,
        ordered: ranks.every((x, i) => i === 0 || ranks[i - 1] <= x),
        ranks: ranks.join(""),
        badgeInText: [...document.querySelectorAll(".page-head p.sub")].some((p) => p.querySelector(".chip, .badge")),
        underButtons: sub && actions ? Math.round(sub.getBoundingClientRect().right) >= Math.round(actions.getBoundingClientRect().right) : null,
        buttonsAfter: sub && actions ? actions.getBoundingClientRect().top >= sub.getBoundingClientRect().bottom : null,
      };
    });
    if (!m) {
      problems.push(`${entry}: no .page-head h1`);
      continue;
    }
    if (!m.inTitle) problems.push(`${entry}: the h1 is not in .page-title`);
    if (m.top !== m.want.top) problems.push(`${entry}: the heading is ${m.top}px below the top of the content, not ${m.want.top}`);
    if (m.gap !== null && m.gap !== m.want.gap) problems.push(`${entry}: the line under the heading is ${m.gap}px below it, not ${m.want.gap}`);
    if (!m.ordered) problems.push(`${entry}: the line is out of order (${m.ranks}; want Archived 0, chips 1, badges 2, text 3)`);
    if (m.badgeInText) problems.push(`${entry}: a chip or badge inside a <p class="sub"> sentence`);
    if (!m.narrow && m.underButtons === false) problems.push(`${entry}: the line stops short of the buttons instead of running under them`);
    if (m.narrow && m.buttonsAfter === false) problems.push(`${entry}: in a narrow column the buttons come before the line`);
  }
  expect(problems).toEqual([]);
});

test("every page's spacing follows the scale", async ({ page }, testInfo) => {
  // docs/design.md#5-spacing. The page had twice the space under it as
  // above (and a section's margin on top of that), stock sections were 20px
  // apart and tiles 12px while other cards were 16px, card parts sat 18 or
  // 19px in (13px for a phone's stock rows), the item's History table was
  // wider than its card, and a heading bar grew with a button or shrank
  // without chips. "desktop" is the sidebar's shell (desktop and laptop).
  const desktop = !phone(testInfo);
  const link = async (url: string, selector: string) => {
    await page.goto(url);
    return (await page.locator(selector).first().getAttribute("href", { timeout: 5_000 })) as string;
  };
  const item = await link("/stock?q=Silkolene", ".item-row a.item-name");
  const vehicle = await link("/vehicles", "a.vehicle-card");
  const kit = await link("/services", "a.kit-row");
  const service = await link("/services", 'a.title[href^="/services/"]');
  const movement = await link("/activity", 'a[href^="/movements/"]');
  const paths = ["/", "/stock", "/stock?view=list", "/stock?low=1", "/stock?archived=1", item, item.replace("/items/", "/activity?item="), "/items/new", item + "/edit",
    "/vehicles", "/vehicles?archived=1", vehicle, "/vehicles/new", vehicle + "/edit", "/services", "/services?archived=1", service, "/services/new", service + "/edit",
    kit, "/kits/new", kit + "/edit", "/shopping", "/activity", movement, "/trash", "/no-such-page"];
  const working = ["/", "/stock", "/services", "/activity", "/shopping", item];
  const problems: string[] = [];
  let cookie = false;
  for (const entry of [...paths, ...working.map((p) => "working " + p)]) {
    const p = entry.replace(/^working /, "");
    if (entry !== p && !cookie) {
      await workingCookie(page, testInfo.project.use.baseURL as string, "KTM 390");
      cookie = true;
    }
    await page.goto(p);
    const found = await page.evaluate((desktop) => {
      const out: string[] = [];
      const root = getComputedStyle(document.documentElement);
      const gap = parseFloat(root.getPropertyValue("--gap"));
      const inset = parseFloat(root.getPropertyValue("--inset"));
      const px = (el: Element, prop: string) => parseFloat(getComputedStyle(el).getPropertyValue(prop)) || 0;
      const shown = (el: Element) => {
        const s = getComputedStyle(el);
        return s.display !== "none" && s.position !== "fixed" && s.position !== "absolute" && el.getBoundingClientRect().height > 0;
      };
      const name = (el: Element) => el.tagName.toLowerCase() + [...el.classList].map((c) => "." + c).join("");
      const main = document.querySelector(".main") as HTMLElement;
      // The same space on the page's sides and under it as above it (and
      // on a phone the floating tab bar's space: its height and the 10px
      // under it), and nothing at the end adding a margin: the main column
      // fitted to its content, its last box ends at its padding.
      const pt = px(main, "padding-top"), pb = px(main, "padding-bottom");
      const tabbar = desktop ? 0 : Math.round(window.innerHeight - document.querySelector(".tabbar")!.getBoundingClientRect().top);
      if (pb !== pt + tabbar) out.push(`the page has ${pb}px under it and ${pt}px above (and the tab bar's ${tabbar}px)`);
      const pl = px(main, "padding-left"), pr = px(main, "padding-right");
      if (pl !== pt || pr !== pt) out.push(`the page has ${pl}px on the left and ${pr}px on the right, ${pt}px above`);
      // It fills the width: no empty space beyond its padding on a wide window.
      const right = Math.round(main.getBoundingClientRect().right);
      if (right !== document.documentElement.clientWidth) out.push(`the page ends at ${right}px in a ${document.documentElement.clientWidth}px window`);
      main.style.alignSelf = "start";
      const kids = [...main.children].filter(shown);
      const end = Math.max(...kids.map((k) => k.getBoundingClientRect().bottom));
      const left = Math.round(main.getBoundingClientRect().bottom - pb - end);
      main.style.alignSelf = "";
      if (left !== 0) out.push(`${left}px of margin under the last box (${name(kids[kids.length - 1])})`);
      // A page that is a form stops at --form-width (or the page's width).
      const form = document.querySelector(".main > .page-form");
      if (form) {
        const want = Math.min(parseFloat(root.getPropertyValue("--form-width")), main.clientWidth - pt * 2);
        const w = Math.round(form.getBoundingClientRect().width);
        if (w !== want) out.push(`the form is ${w}px wide, not ${want}`);
      }
      // Cards, sections, tiles and columns side by side or one under the
      // other are --gap apart.
      const block = ".card, .split > *, .grid > *, .tiles > *, .stack > *";
      // A results heading ("13 items") is nearer what it heads: 18px above
      // it, 10px below.
      for (const h of document.querySelectorAll(".results-head")) {
        const above = h.previousElementSibling, below = h.nextElementSibling, r = h.getBoundingClientRect();
        if (above && shown(above) && Math.round(r.top - above.getBoundingClientRect().bottom) !== 18)
          out.push(`a results heading is ${Math.round(r.top - above.getBoundingClientRect().bottom)}px below ${name(above)}, not 18`);
        if (below && shown(below) && Math.round(below.getBoundingClientRect().top - r.bottom) !== 10)
          out.push(`${name(below)} is ${Math.round(below.getBoundingClientRect().top - r.bottom)}px below its results heading, not 10`);
      }
      for (const b of document.querySelectorAll(block)) {
        const a = b.previousElementSibling;
        if (!a || !a.matches(block) || !shown(a) || !shown(b) || a.matches(".results-head") || b.matches(".results-head")) continue;
        const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
        let d: number | null = null;
        if (rb.top >= ra.bottom - 1 && rb.left < ra.right && ra.left < rb.right) d = rb.top - ra.bottom;
        else if (rb.left >= ra.right - 1 && rb.top < ra.bottom && ra.top < rb.bottom) d = rb.left - ra.right;
        if (d !== null && Math.round(d) !== gap) out.push(`${name(a)} and ${name(b)} are ${Math.round(d)}px apart, not ${gap}`);
      }
      // Each part of a card starts and ends --inset inside its border:
      // its own padding, or the table's first and last cells.
      const part = [".card-head", ".section-head", ".card-body", ".card-pad", ".form-section", ".filters", ".list > li", ".kit-row", ".item-row",
        ".card > .pager", ".card-foot", ".tabs", ".vehicle-card > .body"].join(", ");
      for (const el of document.querySelectorAll(part)) {
        const card = el.closest(".card");
        if (!card || !shown(el) || el.matches(".tile")) continue;
        const outer = el.parentElement?.closest(part);
        if (outer && card.contains(outer)) continue; // counted with the part it is in
        const rc = card.getBoundingClientRect(), r = el.getBoundingClientRect();
        const l = Math.round(r.left + px(el, "padding-left") + px(el, "border-left-width") - rc.left - px(card, "border-left-width"));
        const rr = Math.round(rc.right - px(card, "border-right-width") - (r.right - px(el, "padding-right") - px(el, "border-right-width")));
        if (l !== inset || rr !== inset) out.push(`${name(el)} is ${l}px in on the left and ${rr}px on the right, not ${inset}`);
      }
      // Except the item's photo, which fills its card's top edge to edge.
      for (const el of document.querySelectorAll(".card > .photo")) {
        const card = el.parentElement!, rc = card.getBoundingClientRect(), r = el.getBoundingClientRect();
        const l = Math.round(r.left - rc.left - px(card, "border-left-width")), t = Math.round(r.top - rc.top - px(card, "border-top-width"));
        const rr = Math.round(rc.right - px(card, "border-right-width") - r.right);
        if (l !== 0 || rr !== 0 || t !== 0) out.push(`the photo is ${l}px in on the left, ${rr}px on the right and ${t}px from the top, not edge to edge`);
      }
      for (const table of document.querySelectorAll(".card .table")) {
        const card = table.closest(".card")!;
        const wrap = table.closest(".table-wrap") ?? table;
        // A table fits a card 660px wide or more; a narrower one keeps it
        // 640px and scrolls it inside (design.md#6-layout).
        const rc = card.getBoundingClientRect();
        if (rc.width >= 660 && wrap.scrollWidth > wrap.clientWidth) out.push(`a table is ${wrap.scrollWidth - wrap.clientWidth}px wider than its ${Math.round(rc.width)}px card`);
        for (const row of table.querySelectorAll("tr")) {
          const cells = [...row.children];
          if (cells.length === 0) continue;
          const first = cells[0], last = cells[cells.length - 1];
          const l = Math.round(first.getBoundingClientRect().left + px(first, "padding-left") - rc.left - px(card, "border-left-width"));
          if (l !== inset) out.push(`a table's first column is ${l}px in, not ${inset}`);
          if (wrap.scrollWidth <= wrap.clientWidth) {
            const rr = Math.round(rc.right - px(card, "border-right-width") - (last.getBoundingClientRect().right - px(last, "padding-right")));
            if (rr !== inset) out.push(`a table's last column is ${rr}px in, not ${inset}`);
          }
          break; // one row tells
        }
      }
      // Heading bars are one height, whatever they hold, unless they wrap.
      for (const sel of [".card-head", ".section-head"]) {
        const heights = new Map<number, string>();
        for (const h of document.querySelectorAll(sel)) {
          if (!shown(h)) continue;
          const tops = [...h.children].filter(shown).map((c) => c.getBoundingClientRect().top);
          const bottoms = [...h.children].filter(shown).map((c) => c.getBoundingClientRect().bottom);
          if (tops.length && Math.min(...bottoms) <= Math.max(...tops)) continue; // wrapped onto two lines
          heights.set(Math.round(h.getBoundingClientRect().height), (h.textContent ?? "").trim().slice(0, 30));
        }
        if (heights.size > 1) out.push(`${sel} bars of ${[...heights].map(([hh, t]) => `${hh}px ("${t}")`).join(", ")}`);
      }
      // The Appearance button is a nav row's box: its hover ends on the
      // same edge as the nav's, and it is as tall.
      const theme = [...document.querySelectorAll(".sidebar-head .icon-btn")].find((b) => b.getBoundingClientRect().width > 0);
      const navLink = document.querySelector(".sidebar .nav a");
      if (desktop && theme && navLink) {
        const t = theme.getBoundingClientRect(), n = navLink.getBoundingClientRect();
        if (Math.round(t.right) !== Math.round(n.right) || Math.round(t.height) !== Math.round(n.height))
          out.push(`the Appearance button is ${Math.round(t.height)}px tall ending at ${Math.round(t.right)}px, the nav's rows ${Math.round(n.height)}px ending at ${Math.round(n.right)}px`);
      }
      // The sidebar's logo is level with the page heading's row, a control
      // (36px) tall.
      const mark = document.querySelector(".sidebar .app-brand-mark");
      const head = document.querySelector(".page-head");
      if (desktop && mark && head && !document.querySelector(".working-on")) {
        const m = mark.getBoundingClientRect(), h = head.getBoundingClientRect();
        const off = Math.round((m.top + m.height / 2) - (h.top + 18));
        if (off !== 0) out.push(`the logo is ${off}px off the heading's row`);
      }
      return out;
    }, desktop);
    for (const f of found) problems.push(`${entry}: ${f}`);
  }
  expect(problems).toEqual([]);
});

test("an edit saved in another tab first is not overwritten", async ({ page, context }) => {
  await page.goto("/stock");
  await row(page, "KTM Rear Brake Pads").getByRole("link").first().click();
  const edit = page.url() + "/edit";
  await page.goto(edit);

  const other = await context.newPage();
  await other.goto(edit);
  await other.getByLabel("Notes").fill("From the other tab");
  await other.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(other.locator(".flash-success")).toContainText("Saved");

  // The first tab is refused, keeps what was typed, and offers a reload.
  await page.getByLabel("Notes").fill("From this tab");
  await page.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(page.locator(".error-summary")).toContainText("This item was changed since you opened it");
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
  await page.getByRole("button", { name: "Save item", exact: true }).click();
  await expect(page.locator(".error-summary")).toContainText("was changed since you opened it");
  await page.getByRole("link", { name: "Reload" }).click();
  await page.getByLabel("Notes").fill("");
  await page.getByRole("button", { name: "Save item", exact: true }).click();
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
  await form.getByLabel("Work performed").fill("Chain clean (retried)");

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

test("intermediate schedule on a kit alternates with the full schedule", async ({ page }) => {
  await page.goto("/services");
  await page.locator("a.kit-row").first().click();
  await page.getByRole("link", { name: "Edit", exact: true }).click();

  // Its fields show while the box is ticked.
  await expect(page.getByLabel("Intermediate distance (km)")).toBeHidden();
  await page.getByLabel("Use an intermediate schedule").check();
  await page.getByLabel("Intermediate distance (km)").fill("3500");
  await page.getByRole("button", { name: "More about Intermediate months" }).click();
  await expect(page.locator("#inter-months-tip")).toBeVisible();
  const interItems = page.locator('select[name="inter_line_item"]');
  const interQtys = page.locator('input[name="inter_line_qty"]');
  await interItems.nth(0).selectOption({ label: "Silkolene 15W50 PRO 4 XP" });
  await interQtys.nth(0).fill("1.7 L");
  await page.getByRole("button", { name: "Save kit" }).click();

  // Because a full service was logged at 12 000 km, the next service is Intermediate at 15 500 km.
  await expect(page.locator(".page-head")).toContainText("Next: Intermediate");
  await expect(page.locator("main")).toContainText("Full parts");
  await expect(page.locator("main")).toContainText("Intermediate parts");
  await expect(page.locator('dt:text-is("Due at") + dd')).toHaveText("15\u202f500 km");

  // Clicking "Log service" on the kit pre-selects Intermediate and pre-fills the intermediate parts and title.
  await page.locator(".page-head").getByRole("link", { name: "Log service" }).click();
  await expect(page.getByLabel("Work performed")).toHaveValue("Oil Service (Intermediate)");
  await expect(page.locator('input[name="kit_intermediate"][value="1"]')).toBeChecked();

  // The header names the vehicle (a chip) and the kit (a badge), as plain labels.
  await expect(page.locator(".page-head .chip")).toHaveCount(1);
  await expect(page.locator(".page-head .badge")).toHaveText("Oil Service");
  await expect(page.locator(".page-head a")).toHaveCount(0);
  await expect(page.locator("#vehicle_id")).toHaveCount(0);

  // The intermediate parts are filled in; change one.
  const partItem = page.locator('#part-lines select[name="part_item"]').first();
  const partQty = page.locator('#part-lines input[name="part_qty"]').first();
  await expect(partItem.locator("option:checked")).toHaveText("Silkolene 15W50 PRO 4 XP");
  await partQty.fill("1.5 L");

  // Toggling to Full in the browser switches the title and parts to Full;
  // back to Intermediate brings the rows back as they were typed.
  await page.locator(".segmented label").filter({ hasText: "Full" }).click();
  await expect(page.getByLabel("Work performed")).toHaveValue("Oil Service");
  await expect(partQty).not.toHaveValue("1.5 L");
  await page.locator(".segmented label").filter({ hasText: "Intermediate" }).click();
  await expect(page.getByLabel("Work performed")).toHaveValue("Oil Service (Intermediate)");
  await expect(partItem.locator("option:checked")).toHaveText("Silkolene 15W50 PRO 4 XP");
  await expect(partQty).toHaveValue("1.5 L");
});
