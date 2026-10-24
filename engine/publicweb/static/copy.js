// A Copy button beside an account number on a family's fees page. The
// page works without this script: the number is plain text to select.
(function () {
  "use strict";
  if (!window.navigator.clipboard || (!window.isSecureContext && window.location.hostname !== "localhost")) return;
  var nodes = document.querySelectorAll("[data-copy]");
  for (var i = 0; i < nodes.length; i++) {
    (function (node) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "btn btn-small fam-copy";
      btn.textContent = "Copy";
      btn.setAttribute("aria-label", "Copy the account number " + node.textContent.trim());
      btn.addEventListener("click", function () {
        window.navigator.clipboard.writeText(node.textContent.trim()).then(function () {
          btn.textContent = "Copied";
          setTimeout(function () { btn.textContent = "Copy"; }, 2000);
        });
      });
      node.parentNode.insertBefore(btn, node.nextSibling);
    })(nodes[i]);
  }
})();
