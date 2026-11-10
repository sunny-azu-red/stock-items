// Pitbox — small progressive enhancements. Every page works without
// this file; it only adds convenience (confirmations, dynamic form rows,
// suggestion dropdowns, clipboard, toasts and keyboard shortcuts).
(function () {
  "use strict";

  // ---- Toasts -------------------------------------------------------------
  function toast(message, kind) {
    var box = document.getElementById("toasts");
    if (!box || !message) return;
    var el = document.createElement("div");
    el.className = "toast" + (kind === "error" ? " error" : "");
    el.setAttribute("role", "status");
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

  // ---- Confirmations ------------------------------------------------------
  // <form data-confirm="Delete this?"> asks before submitting (also for htmx).
  document.addEventListener("submit", function (e) {
    var form = e.target;
    var msg = form.getAttribute && form.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) {
      e.preventDefault();
      e.stopImmediatePropagation();
    }
  }, true);

  // ---- Double submits ---------------------------------------------------
  // A double click on "Save" must not log a service (and take its parts from
  // stock) twice. Runs after the confirmation above. htmx forms (the quick
  // -/+ buttons) are meant to be clicked repeatedly and are left alone.
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
        form.classList.contains("theme-toggle")) return;
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
    if (e.target.form && e.target.form.method === "post") dirty = true;
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
        row.querySelectorAll("select").forEach(function (s) { s.selectedIndex = 0; });
        row.querySelectorAll("[data-stock-badge]").forEach(function (n) { n.textContent = ""; });
        row.querySelectorAll(".has-error").forEach(function (el) { el.classList.remove("has-error"); });
        row.querySelectorAll(".field-error").forEach(function (el) { el.remove(); });
        row.querySelectorAll("select[data-part-item]").forEach(updatePartNote);
        syncUnits(row.closest("form"));
      } else {
        row.remove();
        // The rows left may share the removed row's item.
        if (container) updateStockBadges(container);
      }
      return;
    }
    var showInter = e.target.closest("[data-show-inter]");
    if (showInter) {
      e.preventDefault();
      dirty = true;
      var sec = document.getElementById("inter-section");
      var prompt = document.getElementById("inter-prompt");
      var flag = document.getElementById("has_inter");
      if (sec) sec.hidden = false;
      if (prompt) prompt.hidden = true;
      if (flag) flag.value = "1";
      var km = document.getElementById("inter_interval_km");
      if (km) km.focus();
      return;
    }
    var hideInter = e.target.closest("[data-hide-inter]");
    if (hideInter) {
      e.preventDefault();
      dirty = true;
      var sec2 = document.getElementById("inter-section");
      var prompt2 = document.getElementById("inter-prompt");
      var flag2 = document.getElementById("has_inter");
      if (sec2) {
        sec2.hidden = true;
        var interRows = sec2.querySelectorAll("#inter-lines .row-line:not(.row-head)");
        for (var k = 1; k < interRows.length; k++) interRows[k].remove();
        sec2.querySelectorAll("input").forEach(function (i) { i.value = ""; });
        sec2.querySelectorAll("select").forEach(function (s) { s.selectedIndex = 0; });
        sec2.querySelectorAll("[data-stock-badge]").forEach(function (n) { n.textContent = ""; });
        sec2.querySelectorAll(".has-error").forEach(function (el) { el.classList.remove("has-error"); });
        sec2.querySelectorAll(".field-error").forEach(function (el) { el.remove(); });
        sec2.querySelectorAll("select[data-part-item]").forEach(updatePartNote);
      }
      if (prompt2) prompt2.hidden = false;
      if (flag2) flag2.value = "0";
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
    if (e.target.matches("select[data-part-item]")) updatePartNote(e.target);
    if (e.target.matches("select[data-part-unit]")) updateStockBadges(e.target);
    if (e.target.matches("select[data-unit-source]")) followItemUnits(e.target, e.target.form);
    if (e.target.matches("input[data-kit-tier]")) switchKitTier(e.target);
    if (e.target.matches("#vehicle_id")) showKitStarts(e.target);
    if (e.target.matches("input[data-autosubmit]")) sendPhoto(e.target);
    // A picker that filters the page (Activity's year).
    if (e.target.matches("select[data-submit-on-change]")) e.target.form.requestSubmit();
  });

  // On a new service form without a kit, "Start from a kit" lists the
  // chosen vehicle's kits. Picking one opens its form, keeping the date and
  // odometer typed so far.
  function showKitStarts(select) {
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
    var url = new URL(start.href, location.href);
    ["performed_on", "odometer_km"].forEach(function (name) {
      var input = form.elements[name];
      if (input && input.value.trim()) url.searchParams.set(name, input.value.trim());
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
      rows.querySelectorAll("select[data-part-item]").forEach(updatePartNote);
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
  // to do. If the browser can't (HEIC outside Safari, an old browser, any
  // error), the original goes and the server shrinks it. Without JS the
  // form's Upload button sends the original.
  function shrinkPhoto(file, side, force) {
    if (!side || !window.createImageBitmap || !window.DataTransfer || !HTMLCanvasElement.prototype.toBlob) {
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

  // In kit / service part rows, the units and stock badge follow the
  // chosen item.
  function updatePartNote(sel) {
    var row = sel.closest(".row-line");
    if (!row) return;
    followItemUnits(sel, row);
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
  function updateStockBadges(el) {
    var tpl = document.getElementById("stock-badges");
    if (!tpl) return;
    var group = el.closest("[data-stock-need]");
    var row = el.closest(".part-line");
    var rows = group ? group.querySelectorAll(".part-line") : row ? [row] : [];
    var picked = [];
    var need = {};
    Array.prototype.forEach.call(rows, function (r) {
      var sel = r.querySelector("select[data-part-item]");
      var opt = sel && sel.value ? sel.options[sel.selectedIndex] : null;
      var qty = r.querySelector("input[data-part-qty]");
      var unit = r.querySelector("select[data-part-unit]");
      var base = opt ? opt.getAttribute("data-unit") : "";
      var q = opt && group && qty ? readAmount(qty.value, unit ? unit.value : "", base) : null;
      if (q) need[sel.value] = Math.round(((need[sel.value] || 0) + q) * 100) / 100;
      picked.push({ row: r, opt: opt, id: sel ? sel.value : "", base: base });
    });
    picked.forEach(function (p) {
      var slot = p.row.querySelector("[data-stock-badge]");
      if (!slot) return;
      slot.textContent = "";
      var short = false;
      if (p.opt) {
        var have = parseFloat(p.opt.getAttribute("data-have-base")) || 0;
        var n = need[p.id] || 0;
        var level = have <= 0 ? "out" : n > have ? "short" : p.opt.hasAttribute("data-low") ? "low" : "ok";
        short = level === "short" || (level === "out" && n > 0);
        var badge = tpl.content.querySelector('[data-stock-level="' + level + '"]');
        if (badge) {
          badge = badge.cloneNode(true);
          var fill = function (s) {
            return s.replace("{have}", p.opt.getAttribute("data-have"))
              .replace("{need}", formatQty(n, p.base))
              .replace("{min}", p.opt.getAttribute("data-min") || "");
          };
          var last = badge.lastChild;
          if (last && last.nodeType === 3) last.nodeValue = fill(last.nodeValue);
          if (badge.title) badge.title = fill(badge.title);
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

  // Next to an item select, the quantity's hint and unit select follow the
  // chosen item: only its units are offered, preferring the larger one (L, kg).
  function followItemUnits(sel, scope) {
    if (!scope) return;
    var opt = sel.options[sel.selectedIndex];
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
    if (base && (!cur || cur.disabled || !cur.value)) unit.value = last ? last.value : "";
  }

  // ---- Suggestion lists ---------------------------------------------------
  // <input list="…"> gets a dropdown styled like the selects. The browser's
  // datalist popup cannot be styled and mixes in autofill history. Without
  // JS (or popover support) the plain datalist still works. The list is a
  // popover so cards with overflow: clip do not cut it off.
  var placeOpenCombo = null;
  var comboSeed = 0;
  function enhanceCombo(input) {
    var list = document.getElementById(input.getAttribute("list"));
    if (!list || !HTMLElement.prototype.showPopover) return;
    if (input.closest(".combo")) return;
    if (!input.id) input.id = "combo-" + (++comboSeed);
    var values = Array.prototype.map.call(list.options, function (o) { return o.value; });
    if (!values.length) return; // no chevron that opens nothing
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
    function show(filter) {
      var q = filter.trim().toLowerCase();
      box.textContent = "";
      values.forEach(function (v, i) {
        if (q && v.toLowerCase().indexOf(q) < 0) return;
        var li = document.createElement("li");
        li.id = box.id + "-" + i;
        li.setAttribute("role", "option");
        li.textContent = v;
        box.appendChild(li);
      });
      if (!box.children.length) return hide();
      if (!isOpen()) box.showPopover();
      input.setAttribute("aria-expanded", "true");
      placeOpenCombo = place;
      place();
      setActive(-1);
    }
    function hide() {
      if (isOpen()) box.hidePopover();
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
      if (placeOpenCombo === place) placeOpenCombo = null;
    }
    function setActive(i) {
      active = i;
      Array.prototype.forEach.call(box.children, function (li, j) {
        li.setAttribute("aria-selected", j === i ? "true" : "false");
      });
      var li = box.children[i];
      if (li) {
        input.setAttribute("aria-activedescendant", li.id);
        li.scrollIntoView({ block: "nearest" });
      } else {
        input.removeAttribute("aria-activedescendant");
      }
    }
    function pick(li) {
      input.value = li.textContent;
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(new Event("change", { bubbles: true }));
      hide();
    }
    // Below the input, or above it when there is little room below.
    function place() {
      var r = input.getBoundingClientRect();
      var below = window.innerHeight - r.bottom;
      var up = below < 160 && r.top > below;
      box.style.left = r.left + "px";
      box.style.width = r.width + "px";
      box.style.top = up ? "auto" : r.bottom + 4 + "px";
      box.style.bottom = up ? window.innerHeight - r.top + 4 + "px" : "auto";
      box.style.maxHeight = Math.min(256, (up ? r.top : below) - 12) + "px";
    }

    input.addEventListener("input", function () { show(input.value); });
    input.addEventListener("blur", hide);
    input.addEventListener("keydown", function (e) {
      var n = box.children.length;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!isOpen()) return show("");
        setActive(e.key === "ArrowDown" ? (active + 1) % n : (active <= 0 ? n : active) - 1);
      } else if (e.key === "Enter" && isOpen() && active >= 0) {
        e.preventDefault();
        pick(box.children[active]);
      } else if (e.key === "Escape" && isOpen()) {
        e.preventDefault();
        hide();
      }
    });
    // Keep focus in the input while clicking the list or the chevron.
    box.addEventListener("mousedown", function (e) { e.preventDefault(); });
    box.addEventListener("click", function (e) {
      var li = e.target.closest("li");
      if (li) pick(li);
    });
    toggle.addEventListener("mousedown", function (e) { e.preventDefault(); });
    toggle.addEventListener("click", function () {
      if (isOpen()) return hide();
      input.focus();
      show("");
    });
  }
  window.addEventListener("resize", function () { if (placeOpenCombo) placeOpenCombo(); });
  document.addEventListener("scroll", function () { if (placeOpenCombo) placeOpenCombo(); }, true);

  // ---- Clipboard ----------------------------------------------------------
  // navigator.clipboard only exists on HTTPS or localhost, so fall back to
  // execCommand for plain HTTP on the home network.
  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
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
    // Ctrl/Cmd/Shift-click or a middle click (which Firefox reports to the
    // document as a click) opens the text, as on any link.
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    var url = btn.getAttribute("data-copy-url");
    var text = fetch(url, { headers: { Accept: "text/plain" } })
      .then(function (r) { if (!r.ok) throw new Error(r.statusText); return r.text(); })
      .then(function (t) { btn.setAttribute("data-copied", t); return t; });
    var copied;
    if (navigator.clipboard && window.isSecureContext && window.ClipboardItem) {
      // Handing the clipboard a promise keeps this click's permission
      // while the text loads; Safari refuses a write made after the fetch.
      try {
        var blob = text.then(function (t) { return new Blob([t], { type: "text/plain" }); });
        copied = navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })])
          .catch(function () { return text.then(copyText); });
      } catch (err) { // a ClipboardItem that takes no promise
        copied = text.then(copyText);
      }
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

  // ---- Hints --------------------------------------------------------------
  // A "?" shows its hint on hover and keyboard focus (CSS only). A click or
  // tap keeps it open until the next click elsewhere or Escape. Escape also
  // hides a hint shown by hover or focus, until the pointer or focus leaves.
  function closeTips(except) {
    document.querySelectorAll(".tip.open").forEach(function (t) {
      if (t !== except) t.classList.remove("open");
    });
  }
  document.addEventListener("click", function (e) {
    var btn = e.target.closest(".tip-btn");
    closeTips(btn && btn.parentNode);
    if (!btn) return;
    btn.parentNode.classList.remove("dismissed");
    btn.parentNode.classList.toggle("open");
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    closeTips();
    document.querySelectorAll(".tip-btn:hover, .tip-btn:focus").forEach(function (b) {
      b.parentNode.classList.add("dismissed");
    });
  });
  ["mouseout", "focusout"].forEach(function (type) {
    document.addEventListener(type, function (e) {
      var tip = e.target.closest && e.target.closest(".tip");
      if (tip && !tip.contains(e.relatedTarget)) tip.classList.remove("dismissed");
    });
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

  // ---- Theme --------------------------------------------------------------
  // Switch without a reload, then let the server remember it. Picking what
  // the system already shows clears the choice, so the app follows the
  // system again.
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
  }
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.classList || !form.classList.contains("theme-toggle")) return;
    e.preventDefault();
    var theme = e.submitter && e.submitter.value;
    if (theme !== "light" && theme !== "dark") return;
    var systemDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    if ((theme === "dark") === systemDark) theme = "system";
    applyTheme(theme);
    // The button that was clicked is now hidden; keep focus on the toggle.
    form.querySelectorAll("button").forEach(function (b) {
      if (b.offsetParent !== null) b.focus();
    });
    fetch(form.action, {
      method: "POST",
      headers: { "HX-Request": "true", "Content-Type": "application/x-www-form-urlencoded" },
      body: "theme=" + theme
    }).catch(function () { toast("The theme changed here but couldn't be saved.", "error"); });
  });
  // A page that Back restores from the browser's page cache (pageshow with
  // persisted) or reloads from its HTTP cache shows the theme it had when
  // it was left; the choice may have changed on another page since. The
  // cookie the server renders from says what it is now.
  window.addEventListener("pageshow", function () {
    var m = document.cookie.match(/(?:^|;\s*)theme=(light|dark)(?:;|$)/);
    var theme = m ? m[1] : "system";
    if (theme !== (document.documentElement.getAttribute("data-theme") || "system")) applyTheme(theme);
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
    root.querySelectorAll("select[data-part-item]").forEach(updatePartNote);
    root.querySelectorAll('input[list]:not([type="color"])').forEach(enhanceCombo);
  }
  document.addEventListener("DOMContentLoaded", function () {
    init(document);
    if (window.location.hash === "#search" && focusSearch()) {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  });
  document.addEventListener("htmx:load", function (e) { init(e.target); });
})();
