// Stock Items — small progressive enhancements. Every page works without
// this file; it only adds convenience (confirmations, dynamic form rows,
// clipboard, toasts and keyboard shortcuts).
(function () {
  "use strict";
  document.documentElement.classList.add("js");

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

  // ---- Tabs ---------------------------------------------------------------
  document.addEventListener("click", function (e) {
    var tab = e.target.closest('[role="tab"]');
    if (!tab) return;
    var list = tab.closest('[role="tablist"]');
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
  }
  document.addEventListener("DOMContentLoaded", function () {
    init(document);
    if (window.location.hash === "#search" && focusSearch()) {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  });
  document.addEventListener("htmx:load", function (e) { init(e.target); });
})();
