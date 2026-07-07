(function () {
  "use strict";

  var form = document.querySelector("form[data-wizard]");
  if (!form) return;

  var panels = Array.prototype.slice.call(form.querySelectorAll("[data-wizard-panel]"));
  if (!panels.length) return;

  var header = form.querySelector(".wizard-steps");
  var steps = Array.prototype.slice.call(form.querySelectorAll("[data-wizard-step]"));
  var back = form.querySelector("[data-wizard-back]");
  var next = form.querySelector("[data-wizard-next]");
  var submit = form.querySelector("[data-wizard-submit]");
  var current = 0;

  form.classList.add("wizard-enhanced");
  if (header) header.hidden = false;

  panels.some(function (panel, index) {
    if (panel.querySelector(".errorlist")) {
      current = index;
      return true;
    }
    return false;
  });

  function controls(panel) {
    return Array.prototype.slice.call(panel.querySelectorAll("input, select, textarea"));
  }

  function validateCurrent() {
    var firstInvalid = null;
    controls(panels[current]).some(function (control) {
      if (control.disabled || control.type === "hidden") return false;
      if (!control.checkValidity()) {
        firstInvalid = control;
        return true;
      }
      return false;
    });
    if (firstInvalid) {
      firstInvalid.reportValidity();
      return false;
    }
    return true;
  }

  function show(index) {
    current = Math.max(0, Math.min(index, panels.length - 1));
    panels.forEach(function (panel, panelIndex) {
      panel.classList.toggle("is-hidden", panelIndex !== current);
    });
    steps.forEach(function (step, stepIndex) {
      var active = stepIndex === current;
      step.classList.toggle("is-active", active);
      if (active) step.setAttribute("aria-current", "step");
      else step.removeAttribute("aria-current");
    });
    if (back) back.hidden = current === 0;
    if (next) next.hidden = current === panels.length - 1;
    if (submit) submit.hidden = current !== panels.length - 1;
  }

  if (back) {
    back.addEventListener("click", function () {
      show(current - 1);
    });
  }
  if (next) {
    next.addEventListener("click", function () {
      if (validateCurrent()) show(current + 1);
    });
  }
  steps.forEach(function (step, index) {
    step.addEventListener("click", function () {
      if (index <= current || validateCurrent()) show(index);
    });
  });
  form.addEventListener("submit", function (event) {
    if (current < panels.length - 1) {
      event.preventDefault();
      if (validateCurrent()) show(current + 1);
    }
  });

  show(current);
})();
