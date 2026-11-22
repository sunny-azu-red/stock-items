// Pitbox — the behaviour HTML and CSS can't give on their own (current
// Chrome; see AGENTS.md): repeating form rows, unit pickers and stock
// badges, suggestion dropdowns, photo shrinking, clipboard, toasts, tabs,
// the Appearance panel without a reload, the double-submit guard and
// keyboard shortcuts. Dialogs, popovers, the "?" hints and the kit form's
// intermediate schedule need none of it. JavaScript is assumed
// (docs/conventions.md); the server never relies on what it did.
(function () {
  "use strict";

  // ---- Toasts -------------------------------------------------------------
  function toast(message, kind) {
    var box = document.getElementById("toasts");
    if (!box || !message) return;
    var el = document.createElement("div");
    el.className = "toast" + (kind === "error" ? " error" : "");
    el.textContent = message;
    box.appendChild(el);
    setTimeout(function () { el.remove(); }, kind === "error" ? 6000 : 3000);
  }
  // Server sends `HX-Trigger: {"toast": {"message": "...", "kind": "success"}}`.
  document.addEventListener("toast", function (e) {
    var d = e.detail || {};
    toast(d.message, d.kind);
  });
  document.addEventListener("htmx:responseError", function () {
    toast("Pitbox hit an error. Try again.", "error");
  });
  document.addEventListener("htmx:sendError", function () {
    toast("Can't reach the server.", "error");
  });

  // Preserve keyboard focus across htmx out-of-band swap of sort direction.
  var focusedSortDir = null;
  document.addEventListener("htmx:beforeRequest", function () {
    var active = document.activeElement;
    if (active && (active.id === "dir-asc" || active.id === "dir-desc")) {
      focusedSortDir = active.id;
    } else {
      focusedSortDir = null;
    }
  });
  document.addEventListener("htmx:afterSettle", function () {
    if (focusedSortDir) {
      var el = document.getElementById(focusedSortDir);
      if (el) el.focus();
      focusedSortDir = null;
    }
  });

  // ---- Double submits ---------------------------------------------------
  // A double click on "Save" must not log a service (and take its parts from
  // stock) twice. htmx forms (the quick -/+ buttons) are meant to be
  // clicked repeatedly and are left alone.
  //
  // A form with a one-time key (form_key) says "Saving…" on its button. Still
  // on the page after 15 s (a slow or lost connection), it unlocks and says
  // "Still saving… try again": the server saves a key at most once, so a
  // retry of a save that went through only leads to what it saved.
  var retryAfter = 15000;
  var pending = []; // [form, button, timer, original button HTML]
  function submitButton(form, submitter) {
    // Enter "clicks" the hidden plain-save button of the new item form.
    if (submitter && !submitter.classList.contains("sr-only")) return submitter;
    return form.querySelector('button[type="submit"].btn-primary') ||
      form.querySelector('button[type="submit"]:not(.sr-only)');
  }
  function label(btn, text) {
    var icon = btn.querySelector("svg");
    btn.textContent = "";
    if (icon) btn.append(icon, " ");
    btn.append(text);
  }
  function settle(form) {
    pending = pending.filter(function (p) {
      if (p[0] !== form) return true;
      clearTimeout(p[2]);
      if (p[1]) p[1].innerHTML = p[3];
      return false;
    });
  }
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (e.defaultPrevented || form.method !== "post" || form.hasAttribute("hx-post") ||
        form.closest(".appearance")) return;
    if (form.dataset.submitting) {
      e.preventDefault();
      return;
    }
    form.dataset.submitting = "1";
    if (!form.querySelector('input[name="form_key"]')) return;
    var btn = submitButton(form, e.submitter);
    var original = btn ? btn.innerHTML : "";
    pending.forEach(function (p) {
      if (p[0] === form && p[1] === btn) original = p[3]; // a retry: keep the first label
    });
    settle(form);
    if (btn) label(btn, "Saving…");
    var timer = setTimeout(function () {
      delete form.dataset.submitting;
      if (btn) label(btn, "Still saving… try again");
    }, retryAfter);
    pending.push([form, btn, timer, original]);
  });
  // Coming back with the back button restores the page as it was left.
  window.addEventListener("pageshow", function () {
    document.querySelectorAll("form[data-submitting]").forEach(function (f) {
      delete f.dataset.submitting;
    });
    document.querySelectorAll("[data-photo-label][data-text]").forEach(function (l) {
      l.textContent = l.dataset.text; // not "Uploading…" any more
    });
    pending.slice().forEach(function (p) { settle(p[0]); });
  });

  // Typing in a form (other than the stock filters) marks the page as
  // having unsaved changes, so the "a" and "l" shortcuts ask before leaving.
  var dirty = false;
  document.addEventListener("input", function (e) {
    // The Appearance panel saves as it goes: trying a colour isn't typing.
    if (e.target.form && e.target.form.method === "post" && !e.target.closest(".appearance")) dirty = true;
    if (e.target.matches("input[data-part-qty]")) updateStockBadges(e.target);
  });
  // A form sent back with errors still holds what was typed, unsaved.
  document.querySelectorAll('form[method="post"]').forEach(function (f) {
    if (f.querySelector(".has-error, .error-summary")) dirty = true;
  });

  // ---- Repeating form rows ------------------------------------------------
  // <button data-add-row="tpl-id" data-rows="container-id"> clones a <template>.
  document.addEventListener("click", function (e) {
    var add = e.target.closest("[data-add-row]");
    if (add) {
      e.preventDefault();
      var tpl = document.getElementById(add.getAttribute("data-add-row"));
      var rows = document.getElementById(add.getAttribute("data-rows"));
      if (!tpl || !rows) return;
      var frag = tpl.content.cloneNode(true);
      rows.appendChild(frag);
      syncUnits(rows.closest("form"));
      var added = rows.lastElementChild;
      if (added) added.querySelectorAll('input[list]:not([type="color"])').forEach(enhanceCombo);
      var focus = added && added.querySelector("input, select");
      if (focus) focus.focus();
      return;
    }
    var rm = e.target.closest("[data-remove-row]");
    if (rm) {
      e.preventDefault();
      var row = rm.closest(".row-line");
      var container = row && row.parentElement;
      if (!row) return;
      dirty = true;
      // Keep at least one row: clear it instead of removing it.
      if (container && container.querySelectorAll(".row-line:not(.row-head)").length <= 1) {
        row.classList.remove("has-error");
        row.querySelectorAll("input").forEach(function (i) { i.value = ""; });
        row.querySelectorAll("select").forEach(function (s) { s.selectedIndex = 0; delete s.dataset.before; });
        row.querySelectorAll("[data-stock-badge]").forEach(function (n) { n.textContent = ""; });
        row.querySelectorAll(".has-error").forEach(function (el) { el.classList.remove("has-error"); });
        row.querySelectorAll(".field-error").forEach(function (el) { el.remove(); });
        row.querySelectorAll("input[data-item-pick]").forEach(followItem);
        syncUnits(row.closest("form"));
      } else {
        row.remove();
        // The rows left may share the removed row's item.
        if (container) updateStockBadges(container);
      }
      return;
    }
  });

  // ---- Unit pickers -------------------------------------------------------
  // In the item form, the base unit radio decides which sub units
  // (pcs / ml, L / g, kg) the quantity selects offer.
  function syncUnits(form) {
    if (!form) return;
    var checked = form.querySelector('input[name="unit"]:checked');
    if (!checked) return;
    var base = checked.value;
    var price = form.querySelector("#total_price");
    if (price) {
      if (base === "piece") price.setAttribute("aria-describedby", "price-tip");
      else price.removeAttribute("aria-describedby");
    }
    form.querySelectorAll("select[data-subunit]").forEach(function (sel) {
      var firstValid = null;
      Array.prototype.forEach.call(sel.options, function (o) {
        var ok = o.getAttribute("data-base") === base;
        o.hidden = !ok;
        o.disabled = !ok;
        if (ok && !firstValid) firstValid = o;
      });
      var cur = sel.options[sel.selectedIndex];
      if (!cur || cur.disabled) {
        // Prefer the larger unit for volumes/weights in pack sizes.
        sel.value = firstValid ? firstValid.value : "";
      }
    });
  }
  document.addEventListener("change", function (e) {
    if (e.target.matches('input[name="unit"]')) syncUnits(e.target.form);
    if (e.target.matches("select[data-part-unit]")) {
      delete e.target.dataset.before; // picked by hand: it stays (followItemUnits)
      updateStockBadges(e.target);
    }
    if (e.target.matches("input[data-kit-tier]")) switchKitTier(e.target);
    if (e.target.matches("#vehicle_id")) showKitStarts(e.target);
    if (e.target.matches("input[data-autosubmit]")) sendPhoto(e.target);
    // A picker that filters the page (Activity's year).
    if (e.target.matches("select[data-submit-on-change]")) e.target.form.requestSubmit();
  });

  // On a new service form without a kit, "Start from a kit" lists the
  // chosen vehicle's kits, and the odometer's placeholder shows the
  // vehicle's last reading. Picking a kit opens its form, keeping the date,
  // odometer and labour changed so far (only those: an untouched form
  // gives the kit's plain link).
  function showKitStarts(select) {
    var odo = select.form && select.form.elements.odometer;
    if (odo) {
      var opt = select.options[select.selectedIndex];
      var last = opt && opt.getAttribute("data-odometer");
      odo.placeholder = last ? "Last reading " + last : "e.g. 15000";
    }
    var sec = document.getElementById("kit-starts");
    if (!sec) return;
    var any = false;
    sec.querySelectorAll("[data-kit-vehicle]").forEach(function (k) {
      k.hidden = k.getAttribute("data-kit-vehicle") !== select.value;
      if (!k.hidden) any = true;
    });
    sec.hidden = !any;
  }
  document.addEventListener("click", function (e) {
    var start = e.target.closest("a[data-kit-start]");
    var form = start && start.closest("form");
    if (!form) return;
    if (!start.hasAttribute("data-href")) start.setAttribute("data-href", start.getAttribute("href"));
    var url = new URL(start.getAttribute("data-href"), location.href);
    ["performed_on", "odometer", "labor_cost"].forEach(function (name) {
      var input = form.elements[name];
      var v = input ? input.value.trim() : "";
      if (v && v !== input.defaultValue.trim()) url.searchParams.set(name, v);
    });
    start.href = url.pathname + url.search;
  });

  // On a new service form for a kit with an intermediate schedule, switching
  // between Full and Intermediate swaps the pre-filled part rows and title.
  // The rows of the schedule left are kept, as typed, and come back when it
  // is chosen again. An edit has no kit templates: its parts and title stay.
  var keptTierRows = {};
  function switchKitTier(radio) {
    var form = radio.form;
    var tpl = document.getElementById(radio.value === "1" ? "kit-inter-parts" : "kit-full-parts");
    var rows = document.getElementById("part-lines");
    if (!form || !tpl || !rows) return;
    var left = document.createDocumentFragment();
    while (rows.firstChild) left.appendChild(rows.firstChild);
    keptTierRows[radio.value === "1" ? "0" : "1"] = left;
    if (keptTierRows[radio.value]) {
      rows.appendChild(keptTierRows[radio.value]);
      delete keptTierRows[radio.value];
    } else {
      rows.appendChild(tpl.content.cloneNode(true));
      rows.querySelectorAll('input[list]:not([type="color"])').forEach(enhanceCombo);
      rows.querySelectorAll("input[data-item-pick]").forEach(followItem);
    }
    var title = form.querySelector("#title");
    var name = radio.getAttribute("data-kit-name");
    if (title && name) {
      var fullTitle = name;
      var interTitle = name + " (Intermediate)";
      if (title.value === fullTitle || title.value === interTitle || !title.value.trim()) {
        title.value = radio.value === "1" ? interTitle : fullTitle;
      }
    }
  }

  // ---- Photos -------------------------------------------------------------
  // A photo input sends itself once a file is chosen. A photo larger than
  // data-shrink (px) is shrunk here first, upright and on white, so a phone
  // uploads a few hundred KB instead of many MB and the server has little
  // to do. If Chrome can't (HEIC, which it doesn't decode, or any error),
  // the original goes and the server shrinks it. Without JS the
  // form's Upload button sends the original.
  function shrinkPhoto(file, side, force) {
    if (!side) {
      return Promise.reject(new Error("can't shrink here"));
    }
    return createImageBitmap(file, { imageOrientation: "from-image" }).then(function (bmp) {
      var scale = Math.min(1, side / Math.max(bmp.width, bmp.height));
      if (scale === 1 && !force) {
        bmp.close();
        return null; // small already: the original is the better copy
      }
      var canvas = document.createElement("canvas");
      canvas.width = Math.max(1, Math.round(bmp.width * scale));
      canvas.height = Math.max(1, Math.round(bmp.height * scale));
      var g = canvas.getContext("2d");
      g.fillStyle = "#fff"; // JPEG has no transparency
      g.fillRect(0, 0, canvas.width, canvas.height);
      g.imageSmoothingQuality = "high";
      g.drawImage(bmp, 0, 0, canvas.width, canvas.height);
      bmp.close();
      return new Promise(function (resolve, reject) {
        canvas.toBlob(function (blob) {
          if (!blob) return reject(new Error("toBlob failed"));
          var name = file.name.replace(/\.[^.]*$/, "") + ".jpg";
          resolve(new File([blob], name, { type: "image/jpeg", lastModified: file.lastModified }));
        }, "image/jpeg", 0.9);
      });
    });
  }
  function sendPhoto(input) {
    var file = input.files && input.files[0];
    var form = input.form;
    if (!file || !form || form.dataset.preparing) return;
    var label = form.querySelector("[data-photo-label]");
    var text = label ? label.textContent : "";
    if (label) label.dataset.text = text;
    form.dataset.preparing = "1";
    var max = Number(input.getAttribute("data-max-bytes"));
    // Only say so when it takes a moment.
    var slow = setTimeout(function () { if (label) label.textContent = "Preparing photo…"; }, 300);
    var done = function (upload) {
      clearTimeout(slow);
      delete form.dataset.preparing;
      if (label) label.textContent = text;
      if (max && upload.size > max) {
        toast(input.getAttribute("data-too-large"), "error");
        input.value = "";
        return;
      }
      if (upload !== file) {
        try {
          var dt = new DataTransfer();
          dt.items.add(upload);
          input.files = dt.files;
        } catch (err) {
          if (max && file.size > max) {
            toast(input.getAttribute("data-too-large"), "error");
            input.value = "";
            return;
          }
        }
      }
      if (label) label.textContent = "Uploading…";
      if (form.requestSubmit) form.requestSubmit(); else form.submit();
    };
    shrinkPhoto(file, Number(input.getAttribute("data-shrink")), max && file.size > max)
      .then(function (small) { done(small || file); }, function () { done(file); });
  }

  // itemOption is the <option> of the item an item field holds (its
  // hidden [data-item-id], beside its input[data-item-pick] in scope), or
  // null for none.
  function itemOption(scope) {
    var hidden = scope.querySelector("[data-item-id]");
    var input = scope.querySelector("input[data-item-pick]");
    var list = input && document.getElementById(input.getAttribute("data-item-pick"));
    if (!hidden || !hidden.value || !list) return null;
    return list.querySelector('option[data-id="' + CSS.escape(hidden.value) + '"]');
  }

  // In kit / service part rows, the units and stock badge follow the
  // chosen item.
  function updatePartNote(el) {
    var row = el.closest(".row-line");
    if (!row) return;
    followItemUnits(itemOption(row), row);
    updateStockBadges(row);
  }

  // ---- Stock badges -------------------------------------------------------
  // A part row shows its item's stock badge, copied from the page's
  // <template id="stock-badges"> (the "stock-badge" partial). In a service
  // form (data-stock-need) saving takes the parts from stock, so the rows of
  // one item are compared, together, with what is in stock. This reads
  // amounts like stock.ParseAmount but never says why one can't be read:
  // such a row asks for nothing here, and saving explains.
  var stockUnits = null;
  function stockConfig() {
    if (stockUnits) return stockUnits;
    var tpl = document.getElementById("stock-badges");
    try {
      stockUnits = JSON.parse(tpl.getAttribute("data-units"));
    } catch (err) {
      stockUnits = { suffixes: {}, units: {}, sep: " " };
    }
    return stockUnits;
  }
  // readAmount is the amount in the base unit, or null.
  function readAmount(text, subUnit, base) {
    var cfg = stockConfig();
    var m = /^([0-9]+(?:\.[0-9]+)?|\.[0-9]+)\s*([a-z]*)\.?$/.exec(text.trim().toLowerCase().replace(/,/g, "."));
    if (!m || !Object.prototype.hasOwnProperty.call(cfg.suffixes, m[2])) return null;
    var sub = cfg.suffixes[m[2]] || subUnit;
    var factor = 1;
    if (sub) {
      var su = cfg.units[sub];
      if (!su || su.base !== base) return null;
      factor = su.factor;
    }
    // "1.500" could mean 1500 or 1.5 (stock.CheckAmbiguous).
    if (factor === 1 && /^[1-9][0-9]{0,2}\.[0-9]{3}$/.test(m[1])) return null;
    return Math.round(parseFloat(m[1]) * factor * 100) / 100;
  }
  // formatQty is stock.FormatQuantity: "1.7 L", "1 525 ml", "3 pcs".
  function formatQty(q, base) {
    var cfg = stockConfig();
    var small = base, large = "";
    Object.keys(cfg.units).forEach(function (k) {
      var u = cfg.units[k];
      if (u.base !== base) return;
      if (u.factor === 1) small = u.label;
      else large = u.label;
    });
    var r = Math.round(q * 100) / 100;
    if (large && r >= 1000 && r % 10 === 0) return String(Math.round(r) / 1000) + " " + large;
    var parts = String(r).split(".");
    parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, cfg.sep);
    return parts.join(".") + " " + small;
  }
  // stockBadgeFor is the stock badge of an item's <option> (data-have,
  // data-have-base, data-low, data-min, data-unit) when need (base units, 0
  // for none) is asked of it: a copy of the page's template, filled in.
  function stockBadgeFor(opt, need) {
    var tpl = document.getElementById("stock-badges");
    if (!tpl) return null;
    var have = parseFloat(opt.getAttribute("data-have-base")) || 0;
    var level = have <= 0 ? "out" : need > have ? "short" : opt.hasAttribute("data-low") ? "low" : "ok";
    var badge = tpl.content.querySelector('[data-stock-level="' + level + '"]');
    if (!badge) return null;
    badge = badge.cloneNode(true);
    var fill = function (s) {
      return s.replace("{have}", opt.getAttribute("data-have"))
        .replace("{need}", formatQty(need, opt.getAttribute("data-unit")))
        .replace("{min}", opt.getAttribute("data-min") || "");
    };
    var last = badge.lastChild;
    if (last && last.nodeType === 3) last.nodeValue = fill(last.nodeValue);
    if (badge.title) badge.title = fill(badge.title);
    return badge;
  }
  function updateStockBadges(el) {
    if (!document.getElementById("stock-badges")) return;
    var group = el.closest("[data-stock-need]");
    var row = el.closest(".part-line");
    var rows = group ? group.querySelectorAll(".part-line") : row ? [row] : [];
    var picked = [];
    var need = {};
    Array.prototype.forEach.call(rows, function (r) {
      var opt = itemOption(r);
      var id = opt ? opt.getAttribute("data-id") : "";
      var qty = r.querySelector("input[data-part-qty]");
      var unit = r.querySelector("select[data-part-unit]");
      var base = opt ? opt.getAttribute("data-unit") : "";
      var q = opt && group && qty ? readAmount(qty.value, unit ? unit.value : "", base) : null;
      if (q) need[id] = Math.round(((need[id] || 0) + q) * 100) / 100;
      picked.push({ row: r, opt: opt, id: id });
    });
    picked.forEach(function (p) {
      var slot = p.row.querySelector("[data-stock-badge]");
      if (!slot) return;
      slot.textContent = "";
      var short = false;
      if (p.opt) {
        var n = need[p.id] || 0;
        var badge = stockBadgeFor(p.opt, n);
        if (badge) {
          var level = badge.getAttribute("data-stock-level");
          short = level === "short" || (level === "out" && n > 0);
          slot.appendChild(badge);
        }
      }
      // A row a save marked short stays marked until it isn't.
      if (p.row.hasAttribute("data-short") && !short && !p.row.querySelector(".field-error")) {
        p.row.classList.remove("has-error");
        p.row.removeAttribute("data-short");
      }
    });
  }

  // Next to an item picker, the quantity's hint and unit select follow the
  // chosen item's <option> (null for none): only its units are offered,
  // preferring the larger one (L, kg). A unit the item chose goes back to
  // the one before it once no item is chosen; one picked by hand stays
  // (data-before holds the unit to go back to while the item's applies).
  function followItemUnits(opt, scope) {
    if (!scope) return;
    var qty = scope.querySelector("input[data-part-qty]");
    if (qty) {
      // Keep the generic hint while no item is chosen.
      if (qty.dataset.defaultPlaceholder === undefined) qty.dataset.defaultPlaceholder = qty.placeholder;
      qty.placeholder = (opt && opt.getAttribute("data-placeholder")) || qty.dataset.defaultPlaceholder;
    }
    var unit = scope.querySelector("select[data-part-unit]");
    if (!unit) return;
    var base = (opt && opt.getAttribute("data-unit")) || "";
    var last = null;
    Array.prototype.forEach.call(unit.options, function (o) {
      var ok = base ? o.getAttribute("data-base") === base : true;
      o.hidden = !ok;
      o.disabled = !ok;
      if (ok && o.value) last = o;
    });
    var cur = unit.options[unit.selectedIndex];
    if (base && (!cur || cur.disabled || !cur.value)) {
      if (unit.dataset.before === undefined) unit.dataset.before = unit.value;
      unit.value = last ? last.value : "";
    } else if (!base && unit.dataset.before !== undefined) {
      unit.value = unit.dataset.before;
    }
    if (!base) delete unit.dataset.before;
  }

  // ---- Suggestion lists ---------------------------------------------------
  // <input list="…"> gets a dropdown styled like the selects. The browser's
  // datalist popup cannot be styled and mixes in autofill history. Without
  // JS the plain datalist still works. The list is a
  // popover so cards with overflow: clip do not cut it off.
  // An option for a stock item (data-have, as the part rows' options) shows
  // its stock badge. With data-new-entry ("Something new: “{text}”") on the
  // input, the list ends with that entry for text that names no option.
  // Picking an entry fires "combo-pick" on the input, its <option> (null
  // for the new entry) as the detail, before "input" and "change".
  var comboSeed = 0;
  // namesOption is stock.NamesItem for an <option>: the text is its name
  // (data-name, else its value) or "brand name", ignoring case and spaces.
  function namesOption(text, opt) {
    var t = text.trim().toLowerCase();
    if (!t) return false;
    var name = opt.hasAttribute("data-name") ? opt.getAttribute("data-name") : opt.value;
    var brand = opt.getAttribute("data-brand") || "";
    return t === name.trim().toLowerCase() || t === (brand + " " + name).trim().toLowerCase();
  }
  function enhanceCombo(input) {
    var list = document.getElementById(input.getAttribute("list"));
    if (!list) return;
    if (input.closest(".combo")) return;
    if (!input.id) input.id = "combo-" + (++comboSeed);
    var entries = Array.prototype.slice.call(list.options);
    if (!entries.length) return; // no chevron that opens nothing
    var newEntry = input.getAttribute("data-new-entry");
    var wrap = document.createElement("div");
    wrap.className = "combo";
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);
    var toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "combo-toggle";
    toggle.tabIndex = -1;
    toggle.setAttribute("aria-hidden", "true");
    var box = document.createElement("ul");
    box.id = input.id + "-suggestions";
    box.className = "combo-list";
    box.setAttribute("role", "listbox");
    box.setAttribute("popover", "manual");
    wrap.append(toggle, box);
    input.removeAttribute("list");
    input.setAttribute("autocomplete", "off");
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-controls", box.id);
    input.setAttribute("aria-expanded", "false");

    var active = -1;
    function isOpen() { return box.matches(":popover-open"); }
    function options() { return box.querySelectorAll('[role="option"]'); }
    function show(filter) {
      var q = filter.trim().toLowerCase();
      box.textContent = "";
      var group = null;
      var into = box;
      entries.forEach(function (o, i) {
        if (q && o.value.toLowerCase().indexOf(q) < 0) return;
        // Each group's entries (a vehicle's items, then the others) in a
        // group named by its heading, as a <select>'s optgroup.
        var g = o.getAttribute("data-group");
        if (g && g !== group) {
          var wrapper = document.createElement("li");
          var head = document.createElement("div");
          var sub = document.createElement("ul");
          head.className = "combo-group";
          head.id = box.id + "-group-" + i;
          head.textContent = g;
          wrapper.setAttribute("role", "group");
          wrapper.setAttribute("aria-labelledby", head.id);
          sub.setAttribute("role", "none");
          wrapper.append(head, sub);
          box.appendChild(wrapper);
          into = sub;
        } else if (!g) {
          into = box;
        }
        group = g;
        var li = document.createElement("li");
        li.id = box.id + "-" + i;
        li.setAttribute("role", "option");
        li.setAttribute("data-entry", i);
        var badge = o.hasAttribute("data-have") && stockBadgeFor(o, 0);
        if (badge) {
          var text = document.createElement("span");
          text.className = "combo-text";
          text.textContent = o.value;
          li.append(text, badge);
        } else {
          li.textContent = o.value;
        }
        into.appendChild(li);
      });
      if (newEntry && q && !entries.some(function (o) { return namesOption(filter, o); })) {
        var li = document.createElement("li");
        li.id = box.id + "-new";
        li.className = "combo-new";
        li.setAttribute("role", "option");
        li.textContent = newEntry.replace("{text}", filter.trim());
        box.appendChild(li);
      }
      if (!options().length) return hide();
      if (!isOpen()) box.showPopover({ source: input }); // app.css anchors it there
      input.setAttribute("aria-expanded", "true");
      setActive(-1);
    }
    function hide() {
      if (isOpen()) box.hidePopover();
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
    }
    function setActive(i) {
      active = i;
      var opts = options();
      Array.prototype.forEach.call(opts, function (li, j) {
        li.setAttribute("aria-selected", j === i ? "true" : "false");
      });
      var li = opts[i];
      if (li) {
        input.setAttribute("aria-activedescendant", li.id);
        // A group's first entry brings its heading into view too.
        var head = !li.previousElementSibling && li.parentElement !== box && li.parentElement.previousElementSibling;
        if (head) head.scrollIntoView({ block: "nearest" });
        li.scrollIntoView({ block: "nearest" });
      } else {
        input.removeAttribute("aria-activedescendant");
      }
    }
    function pick(li) {
      var opt = li.hasAttribute("data-entry") ? entries[Number(li.getAttribute("data-entry"))] : null;
      if (opt) input.value = opt.value;
      else input.value = input.value.trim(); // the new entry: what was typed
      input.dispatchEvent(new CustomEvent("combo-pick", { bubbles: true, detail: opt }));
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      hide();
    }

    input.addEventListener("input", function () { show(input.value); });
    input.addEventListener("blur", hide);
    input.addEventListener("keydown", function (e) {
      var n = options().length;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!isOpen()) return show(input.value);
        setActive(e.key === "ArrowDown" ? (active + 1) % n : (active <= 0 ? n : active) - 1);
      } else if (e.key === "Enter" && isOpen() && active >= 0) {
        e.preventDefault();
        pick(options()[active]);
      } else if (e.key === "Enter" && isOpen() && input.hasAttribute("data-item-pick")) {
        // An item field searches as it is typed in: Enter with nothing
        // picked closes the list rather than saving half a name.
        e.preventDefault();
        hide();
      } else if (e.key === "Escape" && isOpen()) {
        e.preventDefault();
        hide();
      }
    });
    // Keep focus in the input while clicking the list or the chevron.
    box.addEventListener("mousedown", function (e) { e.preventDefault(); });
    box.addEventListener("click", function (e) {
      var li = e.target.closest('li[role="option"]');
      if (li) pick(li);
    });
    toggle.addEventListener("mousedown", function (e) { e.preventDefault(); });
    toggle.addEventListener("click", function () {
      if (isOpen()) return hide();
      input.focus();
      show("");
    });
  }

  // ---- Item fields --------------------------------------------------------
  // <input data-item-pick="datalist-id"> names a stock item (Shopping's
  // Item, a part row's item): the one picked from its list while the text
  // still names it, else the one item the text names, as the server
  // decides (shoppingItem, lineItem). Its id goes in the [data-item-id]
  // beside it, in its .field. A part row's units and stock badge follow
  // (updatePartNote); Shopping's units and the line under the field
  // ([data-item-status]: the item's stock badge, [data-new-status] or
  // [data-many-status]) too.
  // pickedItem is the item the field is ({opt}), or null with the number
  // of items the text names ({named}: several only when it names more
  // than one, which is none of them unless one is picked).
  function pickedItem(input) {
    var list = document.getElementById(input.getAttribute("data-item-pick"));
    var opts = list ? Array.prototype.slice.call(list.options) : [];
    var picked = itemOption(input.closest(".field"));
    if (picked && namesOption(input.value, picked)) return { opt: picked, named: 1 };
    // An archived item is in the list only for the row that has it.
    var named = opts.filter(function (o) { return !o.hasAttribute("data-archived") && namesOption(input.value, o); });
    return { opt: named.length === 1 ? named[0] : null, named: named.length };
  }
  function followItem(input) {
    var field = input.closest(".field");
    var p = pickedItem(input);
    var opt = p.opt;
    field.querySelector("[data-item-id]").value = opt ? opt.getAttribute("data-id") : "";
    if (input.closest(".row-line")) return updatePartNote(input);
    followItemUnits(opt, input.form);
    var status = field.querySelector("[data-item-status]");
    if (!status) return;
    status.querySelectorAll(".badge").forEach(function (b) { b.remove(); });
    var badge = opt && stockBadgeFor(opt, 0);
    if (badge) status.appendChild(badge);
    var many = status.querySelector("[data-many-status]");
    var several = !opt && p.named > 1;
    if (many) {
      if (many.dataset.text === undefined) many.dataset.text = many.textContent;
      many.textContent = many.dataset.text.replace("{n}", p.named);
      many.hidden = !several;
    }
    status.querySelector("[data-new-status]").hidden = !!opt || several || !input.value.trim();
  }
  document.addEventListener("combo-pick", function (e) {
    if (!e.target.matches("input[data-item-pick]")) return;
    e.target.closest(".field").querySelector("[data-item-id]").value = e.detail ? e.detail.getAttribute("data-id") : "";
  });
  document.addEventListener("input", function (e) {
    if (e.target.matches("input[data-item-pick]")) followItem(e.target);
  });

  // ---- Clipboard ----------------------------------------------------------
  // navigator.clipboard only exists on HTTPS or localhost, so fall back to
  // execCommand for plain HTTP on the home network.
  function copyText(text) {
    if (window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
      ta.remove();
      ok ? resolve() : reject(new Error("copy failed"));
    });
  }
  // <a href="….txt" data-copy-url> copies that text; without JS (or when
  // copying fails) the link shows it instead.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-copy-url]");
    if (!btn) return;
    // Ctrl/Cmd/Shift-click or a middle click opens the text, as on any
    // link.
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    var url = btn.getAttribute("data-copy-url");
    var text = fetch(url, { headers: { Accept: "text/plain" } })
      .then(function (r) { if (!r.ok) throw new Error(r.statusText); return r.text(); })
      .then(function (t) { btn.setAttribute("data-copied", t); return t; });
    var copied;
    if (window.isSecureContext) {
      // Handing the clipboard a promise keeps this click's permission
      // while the text loads.
      var blob = text.then(function (t) { return new Blob([t], { type: "text/plain" }); });
      copied = navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })])
        .catch(function () { return text.then(copyText); });
    } else {
      copied = text.then(copyText);
    }
    copied
      .then(function () { toast("Copied to clipboard"); })
      .catch(function () { window.location.href = url; });
  });

  // ---- More part numbers --------------------------------------------------
  // "+3 more" in a stock row links to the item page; with JS it shows the
  // hidden part numbers in place instead.
  document.addEventListener("click", function (e) {
    var more = e.target.closest("[data-reveal-skus]");
    if (!more) return;
    e.preventDefault();
    var extra = more.parentNode.querySelectorAll(".sku[hidden]");
    extra.forEach(function (s) { s.hidden = false; });
    more.remove();
    if (extra.length) extra[0].focus();
  });

  // ---- Tabs ---------------------------------------------------------------
  function selectTab(tab) {
    var list = tab && tab.closest('[role="tablist"]');
    if (!list) return;
    list.querySelectorAll('[role="tab"]').forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", on ? "true" : "false");
      t.tabIndex = on ? 0 : -1;
      var panel = document.getElementById(t.getAttribute("aria-controls"));
      if (panel) panel.hidden = !on;
    });
  }
  document.addEventListener("click", function (e) {
    selectTab(e.target.closest('[role="tab"]'));
  });
  // Left/right arrows move between tabs, like any tab list.
  document.addEventListener("keydown", function (e) {
    var tab = e.target.closest && e.target.closest('[role="tab"]');
    if (!tab || (e.key !== "ArrowLeft" && e.key !== "ArrowRight")) return;
    var tabs = Array.prototype.slice.call(tab.closest('[role="tablist"]').querySelectorAll('[role="tab"]'));
    var next = tabs[(tabs.indexOf(tab) + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length];
    e.preventDefault();
    selectTab(next);
    next.focus();
  });

  // After a form error, start at the first field to fix rather than the
  // autofocused one.
  var firstError = document.querySelector(".has-error input:not([type=hidden]), .has-error select, .has-error textarea");
  if (firstError) firstError.focus();

  // ---- Appearance: theme and accent (theme.go) ---------------------------
  // Both forms work without this (they post and come back); here a change
  // shows on the spot and is saved in the background, so trying colours is
  // instant and the panel stays open.
  function save(url, body, what) {
    return fetch(url, {
      method: "POST",
      headers: { "HX-Request": "true", "Content-Type": "application/x-www-form-urlencoded" },
      body: body
    }).then(function (r) { if (!r.ok) throw new Error(r.status); })
      .catch(function () { toast("The " + what + " changed here but couldn't be saved.", "error"); });
  }
  // applyTheme shows "light", "dark" or "system" (no choice).
  function applyTheme(theme) {
    var root = document.documentElement;
    var meta = document.querySelector('meta[name="color-scheme"]');
    if (theme === "system") {
      root.removeAttribute("data-theme");
      if (meta) meta.content = "light dark";
    } else {
      root.setAttribute("data-theme", theme);
      if (meta) meta.content = theme;
    }
    document.querySelectorAll(".theme-pick button[name=theme]").forEach(function (b) {
      b.setAttribute("aria-pressed", String(b.value === theme));
    });
    syncThemeColor();
  }
  // The browser's own bar takes the accent too (Chrome on Android).
  function syncThemeColor() {
    var mark = document.querySelector(".app-brand-mark");
    var meta = document.querySelector('meta[name="theme-color"]');
    if (mark && meta) meta.content = getComputedStyle(mark).backgroundColor;
  }
  // applyAccent shows hue and intensity, or the default (Indigo) for null,
  // and moves the panel's sliders and swatches to match.
  var DEFAULT_ACCENT = "280-100";
  function applyAccent(value) {
    var root = document.documentElement;
    var v = value || DEFAULT_ACCENT;
    var hi = v.split("-");
    if (value && value !== DEFAULT_ACCENT) {
      root.style.setProperty("--accent-h", hi[0]);
      root.style.setProperty("--accent-i", hi[1]);
    } else {
      root.style.removeProperty("--accent-h");
      root.style.removeProperty("--accent-i");
      if (!root.getAttribute("style")) root.removeAttribute("style");
    }
    document.querySelectorAll(".accent-pick").forEach(function (f) {
      f.elements.hue.value = hi[0];
      f.elements.intensity.value = hi[1];
      f.querySelectorAll(".swatch").forEach(function (b) { b.setAttribute("aria-pressed", String(b.value === v)); });
    });
    syncThemeColor();
  }
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.classList) return;
    if (form.classList.contains("theme-pick")) {
      e.preventDefault();
      var theme = e.submitter && e.submitter.value;
      if (theme !== "light" && theme !== "dark") theme = "system";
      applyTheme(theme);
      save(form.action, "theme=" + theme, "theme");
    } else if (form.classList.contains("accent-pick")) {
      e.preventDefault();
      var s = e.submitter;
      if (s && s.name === "reset") {
        applyAccent(null);
        save(form.action, "reset=1", "accent");
      } else {
        var v = s && s.name === "accent" ? s.value : form.elements.hue.value + "-" + form.elements.intensity.value;
        applyAccent(v);
        save(form.action, "accent=" + encodeURIComponent(v), "accent");
      }
    }
  });
  // The sliders recolour the page as they move, and save once let go.
  document.querySelectorAll(".accent-pick").forEach(function (f) {
    var saveButton = f.querySelector(".accent-save");
    if (saveButton) saveButton.hidden = true;
    f.addEventListener("input", function (e) {
      if (e.target.type !== "range") return;
      applyAccent(f.elements.hue.value + "-" + f.elements.intensity.value);
    });
    f.addEventListener("change", function (e) {
      if (e.target.type === "range") f.requestSubmit();
    });
  });
  syncThemeColor();
  // A page that Back restores from the browser's page cache (pageshow with
  // persisted) or reloads from its HTTP cache shows the theme and accent it
  // had when it was left; the choice may have changed on another page
  // since. The cookies the server renders from say what they are now.
  window.addEventListener("pageshow", function () {
    var m = document.cookie.match(/(?:^|;\s*)theme=(light|dark)(?:;|$)/);
    var theme = m ? m[1] : "system";
    if (theme !== (document.documentElement.getAttribute("data-theme") || "system")) applyTheme(theme);
    var a = document.cookie.match(/(?:^|;\s*)accent=(\d{1,3}-\d{1,3})(?:;|$)/);
    var root = document.documentElement.style;
    var shown = root.getPropertyValue("--accent-h") ? root.getPropertyValue("--accent-h").trim() + "-" + root.getPropertyValue("--accent-i").trim() : null;
    if ((a ? a[1] : null) !== shown) applyAccent(a ? a[1] : null);
  });
  // The same for the vehicle worked on (working.go): it decides what the
  // list pages show, and the bar says so. A page from before a switch
  // reloads, unless it holds unsaved typing. Only the plain digits the
  // server counts (no leading zero), so the two never disagree in a loop.
  window.addEventListener("pageshow", function () {
    var m = document.cookie.match(/(?:^|;\s*)working_on="?([1-9]\d*)"?(?:;|$)/);
    var now = m ? m[1] : "";
    if (now !== (document.documentElement.getAttribute("data-working-on") || "") && !dirty) location.reload();
  });

  // The phone's More sheet (the sidebar) and Switch tray (popovers) close
  // on the way out, so a page that Back brings from the page cache doesn't
  // come with one open.
  window.addEventListener("pagehide", function () {
    ["more", "switch"].forEach(function (id) {
      var p = document.getElementById(id);
      if (p && p.matches(":popover-open")) p.hidePopover();
    });
  });
  // The Switch tray hangs from the bar's padding edge (it covers the bar's
  // bottom border with its own), and the grey starts there (app.css). The
  // bar is sticky, so measure it as it is when the tray opens.
  var switchTray = document.getElementById("switch");
  if (switchTray) switchTray.addEventListener("beforetoggle", function (e) {
    var bar = switchTray.closest(".working-on");
    if (e.newState !== "open" || !bar) return;
    var border = parseFloat(getComputedStyle(bar).borderBottomWidth) || 0;
    switchTray.style.setProperty("--switch-top", Math.round(bar.getBoundingClientRect().bottom - border) + "px");
  });

  // ---- Keyboard shortcuts -------------------------------------------------
  // "/" focuses search (opening the stock page when this page has none),
  // "a" opens Add item and "l" Log service: the first letter of the button
  // each stands for (the sidebar's), on every page alike.
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var t = e.target;
    if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
    if (e.key === "/") {
      e.preventDefault();
      if (!focusSearch()) window.location.href = "/stock#search";
    } else if (e.key === "a" || e.key === "l") {
      if (dirty && !window.confirm("Leave this page? What you typed isn't saved yet.")) return;
      window.location.href = e.key === "a" ? "/items/new" : "/services/new";
    }
  });
  function focusSearch() {
    var s = document.querySelector("[data-search]");
    if (!s) return false;
    s.focus();
    s.select();
    return true;
  }

  // ---- Init ---------------------------------------------------------------
  function init(root) {
    root.querySelectorAll("form").forEach(syncUnits);
    root.querySelectorAll('input[list]:not([type="color"])').forEach(enhanceCombo);
    root.querySelectorAll("input[data-item-pick]").forEach(followItem);
  }
  document.addEventListener("DOMContentLoaded", function () {
    init(document);
    if (window.location.hash === "#search" && focusSearch()) {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  });
  document.addEventListener("htmx:load", function (e) { init(e.target); });
})();
