import { expect, test, type Page } from "@playwright/test";

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

test("filter stock by vehicle and copy it as text", async ({ page }, testInfo) => {
  await page.goto("/stock");
  await expect(page.locator(".section")).toHaveCount(3);

  await page.locator("#filters label.chip-toggle").filter({ hasText: exact("KTM 390") }).click();
  await expect(page).toHaveURL(/[?&]v=\d+/);
  await expect(page.locator(".section")).toHaveCount(2);
  await expect(page.locator(".section").first()).toContainText("Silkolene");
  await expect(page.locator(".section-shared")).toContainText("M3.0 Ready To Use");
  await expect(page.locator("#results")).not.toContainText("Hiflofiltro");
  await page.screenshot({ path: testInfo.outputPath("stock-ktm390.png"), fullPage: true });

  const copy = page.getByRole("button", { name: "Copy as text" });
  await copy.click();
  await expect(copy).toHaveAttribute("data-copied", /KTM 390\n-{12}\n/);
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
  await oil.getByRole("button", { name: /Add one/ }).click();
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("625 ml");
  await row(page, "15W50 PRO 4 XP").getByRole("button", { name: /Use one/ }).click();
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("525 ml");

  await row(page, "15W50 PRO 4 XP").getByRole("link").first().click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Silkolene 15W50 PRO 4 XP");
  const bought = page.locator("#panel-purchase");
  await bought.getByLabel("How much did you buy?").fill("1");
  await bought.locator('select[name="amount_unit"]').selectOption("l");
  await bought.getByLabel("Total price").fill("18,50");
  await bought.getByLabel("Shop").fill("Moto shop");
  await bought.getByRole("button", { name: "Add to stock" }).click();
  await expect(page.locator("#item-qty")).toHaveText("1.525 L");
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
  await expect(page.locator("#item-qty")).toHaveText("1.525 L");
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
  await expect(page.locator("main")).toContainText("12,000 km");

  await page.goto("/stock");
  await expect(row(page, "15W50 PRO 4 XP").locator(".qty")).toHaveText("3.825 L");
  await expect(row(page, "OEM Oil Filter").filter({ hasNotText: "O-Ring" }).locator(".qty")).toHaveText("0x");

  await page.goto("/");
  await expect(page.locator("main")).toContainText("in 7,500 km");
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

test("no page scrolls sideways", async ({ page }) => {
  const paths = ["/", "/stock", "/stock?view=list", "/vehicles", "/services", "/shopping", "/activity", "/items/new", "/kits/new", "/services/new"];
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

// Opt-in: SCREENSHOT_DIR=/tmp/shots npx playwright test -g screenshots (see docs/development.md)
test("screenshots", async ({ page }, testInfo) => {
  const dir = process.env.SCREENSHOT_DIR;
  test.skip(!dir, "set SCREENSHOT_DIR to capture screenshots");
  const shots: [string, string][] = [
    ["dashboard", "/"],
    ["stock", "/stock"],
    ["vehicles", "/vehicles"],
    ["services", "/services"],
    ["shopping", "/shopping"],
    ["activity", "/activity"],
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
  shots.push(["kit-new", "/kits/new"]);
  shots.push(["service-new", "/services/new"]);
  for (const [name, url] of shots) {
    await page.goto(url);
    await page.screenshot({ path: `${dir}/${testInfo.project.name}-${name}.png`, fullPage: true });
  }
});
