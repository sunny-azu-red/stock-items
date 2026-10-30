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
    toast("Something went wrong. Please try again.", "error");
  });
  document.addEventListener("htmx:sendError", function () {
    toast("Cannot reach the server.", "error");
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
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (e.defaultPrevented || form.method !== "post" || form.hasAttribute("hx-post") ||
        form.classList.contains("theme-toggle")) return;
    if (form.dataset.submitting) {
      e.preventDefault();
      return;
    }
    form.dataset.submitting = "1";
  });
  // Coming back with the back button restores the page as it was left.
  window.addEventListener("pageshow", function () {
    document.querySelectorAll("form[data-submitting]").forEach(function (f) {
      delete f.dataset.submitting;
    });
  });

  // Typing in a form (other than the stock filters) marks the page as
  // having unsaved changes, so the "n" shortcut asks before leaving.
  var dirty = false;
  document.addEventListener("input", function (e) {
    if (e.target.form && e.target.form.method === "post") dirty = true;
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
      // Keep at least one row: clear it instead of removing it.
      if (container && container.querySelectorAll(".row-line").length <= 1) {
        row.querySelectorAll("input").forEach(function (i) { i.value = ""; });
        row.querySelectorAll("select").forEach(function (s) { s.selectedIndex = 0; });
      } else {
        row.remove();
      }
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
    if (e.target.matches("input[data-autosubmit]")) {
      var f = e.target.form;
      if (f.requestSubmit) f.requestSubmit(); else f.submit();
    }
  });

  // In kit / service part rows, show what is on stock for the chosen item.
  function updatePartNote(sel) {
    var row = sel.closest(".row-line");
    if (!row) return;
    var note = row.querySelector(".stock-note");
    var opt = sel.options[sel.selectedIndex];
    var qty = row.querySelector("input[data-part-qty]");
    if (note) note.textContent = opt && opt.getAttribute("data-have") ? "on stock: " + opt.getAttribute("data-have") : "";
    if (qty) {
      // Keep the generic hint for rows with no item chosen yet.
      if (qty.dataset.defaultPlaceholder === undefined) qty.dataset.defaultPlaceholder = qty.placeholder;
      qty.placeholder = (opt && opt.getAttribute("data-placeholder")) || qty.dataset.defaultPlaceholder;
    }
  }

  // ---- Suggestion lists ---------------------------------------------------
  // <input list="…"> gets a dropdown styled like the selects. The browser's
  // datalist popup cannot be styled and mixes in autofill history. Without
  // JS (or popover support) the plain datalist still works. The list is a
  // popover so cards with overflow: clip do not cut it off.
  var placeOpenCombo = null;
  function enhanceCombo(input) {
    var list = document.getElementById(input.getAttribute("list"));
    if (!list || !input.id || !HTMLElement.prototype.showPopover) return;
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
  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-copy-url]");
    if (!btn) return;
    e.preventDefault();
    var url = btn.getAttribute("data-copy-url");
    fetch(url, { headers: { Accept: "text/plain" } })
      .then(function (r) { if (!r.ok) throw new Error(r.statusText); return r.text(); })
      .then(function (text) {
        btn.setAttribute("data-copied", text);
        return copyText(text);
      })
      .then(function () { toast("Copied to clipboard"); })
      .catch(function () { window.open(url, "_blank"); });
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
  document.addEventListener("click", function (e) {
    var tab = e.target.closest('[role="tab"]');
    var list = tab && tab.closest('[role="tablist"]');
    if (!list) return;
    list.querySelectorAll('[role="tab"]').forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", on ? "true" : "false");
      var panel = document.getElementById(t.getAttribute("aria-controls"));
      if (panel) panel.hidden = !on;
    });
  });

  // ---- Theme --------------------------------------------------------------
  // Switch without a reload, then let the server remember it. Picking what
  // the system already shows clears the choice, so the app follows the
  // system again.
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.classList || !form.classList.contains("theme-toggle")) return;
    e.preventDefault();
    var theme = e.submitter && e.submitter.value;
    if (theme !== "light" && theme !== "dark") return;
    var systemDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    var root = document.documentElement;
    var meta = document.querySelector('meta[name="color-scheme"]');
    if ((theme === "dark") === systemDark) {
      root.removeAttribute("data-theme");
      if (meta) meta.content = "light dark";
      theme = "system";
    } else {
      root.setAttribute("data-theme", theme);
      if (meta) meta.content = theme;
    }
    // The button that was clicked is now hidden; keep focus on the toggle.
    form.querySelectorAll("button").forEach(function (b) {
      if (b.offsetParent !== null) b.focus();
    });
    fetch(form.action, {
      method: "POST",
      headers: { "HX-Request": "true", "Content-Type": "application/x-www-form-urlencoded" },
      body: "theme=" + theme
    }).catch(function () { toast("Could not save the theme.", "error"); });
  });

  // ---- Keyboard shortcuts -------------------------------------------------
  // "/" focuses search (opening the stock page when this page has none),
  // "n" opens the new item form.
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var t = e.target;
    if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
    if (e.key === "/") {
      e.preventDefault();
      if (!focusSearch()) window.location.href = "/stock#search";
    } else if (e.key === "n") {
      if (dirty && !window.confirm("Leave this page? Your changes are not saved.")) return;
      window.location.href = "/items/new";
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
