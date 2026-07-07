(function () {
  "use strict";

  var island = document.getElementById("activity-type-vocabulary");
  var vocabulary = [];
  if (island) {
    try {
      vocabulary = JSON.parse(island.textContent || "[]");
    } catch (err) {
      vocabulary = [];
    }
  }

  function normalize(value) {
    return String(value || "")
      .normalize("NFD")
      .replace(/[̀-ͯ]/g, "")
      .toLowerCase();
  }

  function optionMeta(select) {
    return Array.prototype.slice.call(select.options)
      .filter(function (option) {
        return option.value !== "";
      })
      .map(function (option) {
        var vocab = vocabulary.find(function (item) {
          return String(item.id || "") === String(option.value);
        });
        if (!vocab) {
          vocab = vocabulary.find(function (item) {
            return normalize(item.name) === normalize(option.textContent);
          });
        }
        return {
          value: option.value,
          label: option.textContent.trim(),
          option: option,
          slug: vocab ? vocab.slug : "",
          aliases: vocab && Array.isArray(vocab.aliases) ? vocab.aliases : [],
          category: vocab ? vocab.category : "",
          categoryName: vocab ? vocab.categoryName : "",
        };
      });
  }

  function makeId(base, suffix) {
    return (base || "combobox") + "-" + suffix;
  }

  function enhance(select) {
    var multiple = select.multiple;
    var max = parseInt(select.dataset.comboboxMax || "2", 10);
    var options = optionMeta(select);
    var selectedValue = select.value;
    var label = select.id ? document.querySelector('label[for="' + select.id + '"]') : null;
    var wrap = document.createElement("div");
    var chips = document.createElement("div");
    var input = document.createElement("input");
    var list = document.createElement("div");
    var hint = document.createElement("div");
    var activeIndex = -1;
    var matches = [];

    wrap.className = "combobox";
    chips.className = "combobox-chips";
    input.type = "text";
    input.className = "combobox-input";
    input.id = makeId(select.id, "input");
    input.autocomplete = "off";
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-controls", makeId(select.id, "listbox"));
    input.disabled = select.disabled;
    list.className = "combobox-list is-hidden";
    list.id = makeId(select.id, "listbox");
    list.setAttribute("role", "listbox");
    hint.className = "helptext combobox-hint";
    hint.setAttribute("aria-live", "polite");

    if (select.required && !multiple) {
      input.required = true;
      select.dataset.comboboxWasRequired = "true";
      select.required = false;
    }
    if (label) label.setAttribute("for", input.id);

    select.classList.add("is-hidden");
    select.setAttribute("aria-hidden", "true");
    select.parentNode.insertBefore(wrap, select.nextSibling);
    if (multiple) wrap.appendChild(chips);
    wrap.appendChild(input);
    wrap.appendChild(list);
    wrap.appendChild(hint);

    function selectedOptions() {
      return options.filter(function (item) {
        return item.option.selected;
      });
    }

    function searchableText(item) {
      return [item.label, item.slug, item.categoryName].concat(item.aliases || []).join(" ");
    }

    function findMatches(query) {
      var q = normalize(query);
      if (!q) return options.slice(0, 8);
      return options
        .filter(function (item) {
          if (multiple && item.option.selected) return false;
          return normalize(searchableText(item)).indexOf(q) !== -1;
        })
        .sort(function (a, b) {
          var ap = normalize(a.label).indexOf(q) === 0 || normalize(a.slug).indexOf(q) === 0;
          var bp = normalize(b.label).indexOf(q) === 0 || normalize(b.slug).indexOf(q) === 0;
          if (ap === bp) return a.label.localeCompare(b.label);
          return ap ? -1 : 1;
        })
        .slice(0, 8);
    }

    function closeList() {
      list.classList.add("is-hidden");
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
      activeIndex = -1;
    }

    function setActive(index) {
      activeIndex = index;
      Array.prototype.slice.call(list.querySelectorAll("[role=option]")).forEach(function (node, nodeIndex) {
        var active = nodeIndex === activeIndex;
        node.classList.toggle("is-active", active);
        node.setAttribute("aria-selected", active ? "true" : "false");
        if (active) input.setAttribute("aria-activedescendant", node.id);
      });
    }

    function renderChips() {
      chips.textContent = "";
      selectedOptions().forEach(function (item) {
        var chip = document.createElement("span");
        var button = document.createElement("button");
        chip.className = "combobox-chip";
        chip.textContent = item.label;
        button.type = "button";
        button.className = "combobox-chip-remove";
        button.setAttribute("aria-label", "Remove " + item.label);
        button.textContent = "×";
        button.addEventListener("click", function () {
          item.option.selected = false;
          select.dispatchEvent(new Event("change", { bubbles: true }));
          renderChips();
          renderList();
          input.focus();
        });
        chip.appendChild(button);
        chips.appendChild(chip);
      });
    }

    function choose(item) {
      if (!item) return;
      if (multiple) {
        if (selectedOptions().length >= max) {
          hint.textContent = "Pick at most " + max + " extra types.";
          closeList();
          return;
        }
        item.option.selected = true;
        input.value = "";
        renderChips();
      } else {
        select.value = item.value;
        selectedValue = item.value;
        input.value = item.label;
        input.setCustomValidity("");
      }
      hint.textContent = "";
      select.dispatchEvent(new Event("change", { bubbles: true }));
      closeList();
    }

    function renderList() {
      var query = input.value;
      list.textContent = "";
      hint.textContent = "";
      if (multiple && selectedOptions().length >= max) {
        hint.textContent = "Pick at most " + max + " extra types.";
        closeList();
        return;
      }
      matches = findMatches(query);
      if (!matches.length && query) {
        var empty = document.createElement("div");
        empty.className = "combobox-option is-disabled";
        empty.setAttribute("role", "option");
        empty.setAttribute("aria-disabled", "true");
        empty.textContent = "No matching type";
        list.appendChild(empty);
        list.classList.remove("is-hidden");
        input.setAttribute("aria-expanded", "true");
        if (!multiple) input.setCustomValidity("Choose a matching type.");
        return;
      }
      matches.forEach(function (item, index) {
        var row = document.createElement("button");
        row.type = "button";
        row.className = "combobox-option";
        row.id = makeId(select.id, "option-" + index);
        row.setAttribute("role", "option");
        row.setAttribute("aria-selected", "false");
        row.textContent = item.label;
        row.addEventListener("mousedown", function (event) {
          event.preventDefault();
        });
        row.addEventListener("click", function () {
          choose(item);
        });
        list.appendChild(row);
      });
      if (matches.length) {
        list.classList.remove("is-hidden");
        input.setAttribute("aria-expanded", "true");
        setActive(0);
      } else {
        closeList();
      }
    }

    function syncSingleFromSelect() {
      var current = options.find(function (item) {
        return item.value === select.value;
      });
      input.value = current ? current.label : "";
      selectedValue = select.value;
    }

    if (multiple) {
      renderChips();
    } else {
      syncSingleFromSelect();
    }

    input.addEventListener("input", function () {
      if (!multiple && input.value && select.value === selectedValue) {
        select.value = "";
      }
      if (!multiple) input.setCustomValidity("");
      renderList();
    });
    input.addEventListener("focus", renderList);
    input.addEventListener("blur", function () {
      window.setTimeout(closeList, 120);
      if (!multiple && input.value && !select.value) {
        input.setCustomValidity("Choose a matching type.");
      }
    });
    input.addEventListener("keydown", function (event) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        if (list.classList.contains("is-hidden")) renderList();
        else setActive(Math.min(activeIndex + 1, matches.length - 1));
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        setActive(Math.max(activeIndex - 1, 0));
      } else if (event.key === "Enter") {
        if (!list.classList.contains("is-hidden")) {
          event.preventDefault();
          choose(matches[activeIndex]);
        }
      } else if (event.key === "Escape") {
        closeList();
      }
    });
    select.addEventListener("change", function () {
      if (multiple) renderChips();
      else syncSingleFromSelect();
    });
  }

  Array.prototype.slice.call(document.querySelectorAll("select[data-combobox]")).forEach(enhance);
})();
