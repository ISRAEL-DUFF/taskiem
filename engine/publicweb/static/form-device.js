// Device fields on a form step's one-time page (formdevice.go). Each is
// asked only when the person taps it, after the question's why: the
// browser's own location prompt; a signature drawn on a pad, sent as a PNG
// file; a code read by the camera where the browser has a barcode reader
// (BarcodeDetector), typed elsewhere. Nothing is read without a tap. No
// outside library: the browser's own APIs only.
(function () {
  "use strict";

  // The browser's barcode names and the server's.
  var names = { qr_code: "qr", ean_13: "ean_13", ean_8: "ean_8", upc_a: "upc_a", upc_e: "upc_e", code_128: "code_128" };
  var words = { qr: "QR", ean_13: "EAN-13", ean_8: "EAN-8", upc_a: "UPC-A", upc_e: "UPC-E", code_128: "Code 128" };

  function q(box, sel) { return box.querySelector(sel); }
  function say(box, text) { var g = q(box, "[data-got]"); if (g) g.textContent = text; }

  function place(box) {
    var go = q(box, "[data-locate]");
    if (!go || !("geolocation" in window.navigator)) return;
    go.hidden = false;
    go.addEventListener("click", function () {
      go.disabled = true;
      say(box, "Reading your location…");
      window.navigator.geolocation.getCurrentPosition(function (p) {
        q(box, "[data-lat]").value = String(p.coords.latitude);
        q(box, "[data-lng]").value = String(p.coords.longitude);
        q(box, "[data-acc]").value = String(Math.round(p.coords.accuracy));
        q(box, "[data-at]").value = new Date(p.timestamp || Date.now()).toISOString().replace(/\.\d+Z$/, "Z");
        q(box, "[data-fix]").value = p.coords.accuracy <= 100 ? "gps" : "network";
        q(box, "[data-state]").value = "";
        say(box, "Your location was read, to within " + Math.round(p.coords.accuracy) + " m.");
        go.textContent = "Read it again";
        go.disabled = false;
      }, function (e) {
        ["[data-lat]", "[data-lng]", "[data-acc]", "[data-at]", "[data-fix]"].forEach(function (s) { q(box, s).value = ""; });
        var refused = e && e.code === 1;
        q(box, "[data-state]").value = refused ? "refused" : "unavailable";
        say(box, refused ? "Your browser was not allowed to read your location." : "Your browser could not read your location.");
        go.disabled = false;
      }, { enableHighAccuracy: true, timeout: 20000, maximumAge: 0 });
    });
  }

  function signature(box) {
    var pad = q(box, "[data-pad]"), canvas = q(box, "[data-canvas]"), file = q(box, "[data-padfile]");
    if (!pad || !canvas || !canvas.getContext || typeof window.DataTransfer === "undefined") return;
    pad.hidden = false;
    var ctx = canvas.getContext("2d"), drawing = false, inked = false;
    function blank() {
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.strokeStyle = "#111111";
      ctx.lineWidth = 3;
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
    }
    blank();
    function at(e) {
      var r = canvas.getBoundingClientRect();
      return { x: (e.clientX - r.left) * canvas.width / r.width, y: (e.clientY - r.top) * canvas.height / r.height };
    }
    function keep() {
      canvas.toBlob(function (b) {
        if (!b) return;
        var dt = new window.DataTransfer();
        dt.items.add(new window.File([b], "signature.png", { type: "image/png" }));
        file.files = dt.files;
      }, "image/png");
    }
    canvas.addEventListener("pointerdown", function (e) {
      drawing = true;
      canvas.setPointerCapture(e.pointerId);
      var p = at(e);
      ctx.beginPath();
      ctx.moveTo(p.x, p.y);
      e.preventDefault();
    });
    canvas.addEventListener("pointermove", function (e) {
      if (!drawing) return;
      var p = at(e);
      ctx.lineTo(p.x, p.y);
      ctx.stroke();
      inked = true;
    });
    function end() {
      if (!drawing) return;
      drawing = false;
      if (inked) keep();
    }
    canvas.addEventListener("pointerup", end);
    canvas.addEventListener("pointercancel", end);
    q(box, "[data-clear]").addEventListener("click", function () {
      blank();
      inked = false;
      file.value = "";
    });
  }

  function scan(box) {
    var go = q(box, "[data-scan]"), cam = q(box, "[data-camera]"), video = q(box, "[data-video]");
    var code = q(box, "[data-code]"), format = q(box, "[data-format]");
    if (code) {
      code.addEventListener("input", function () { format.value = ""; say(box, ""); });
    }
    if (!go || !("BarcodeDetector" in window) || !window.navigator.mediaDevices || !window.navigator.mediaDevices.getUserMedia) return;
    var wanted = (box.getAttribute("data-formats") || "").split(" ").filter(Boolean);
    var stream = null, timer = 0;
    function stop() {
      if (timer) window.clearTimeout(timer);
      timer = 0;
      if (stream) stream.getTracks().forEach(function (t) { t.stop(); });
      stream = null;
      cam.hidden = true;
      go.disabled = false;
    }
    window.BarcodeDetector.getSupportedFormats().then(function (have) {
      var use = have.filter(function (f) { return names[f] && wanted.indexOf(names[f]) >= 0; });
      if (!use.length) return; // the browser reads none of the codes asked: typed
      var detector = new window.BarcodeDetector({ formats: use });
      go.hidden = false;
      go.addEventListener("click", function () {
        go.disabled = true;
        say(box, "Starting the camera…");
        window.navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" }, audio: false }).then(function (s) {
          stream = s;
          video.srcObject = s;
          cam.hidden = false;
          say(box, "Hold the code in front of the camera.");
          return video.play();
        }).then(function look() {
          if (!stream) return;
          detector.detect(video).then(function (found) {
            var hit = (found || []).filter(function (c) { return names[c.format] && wanted.indexOf(names[c.format]) >= 0; })[0];
            if (hit) {
              code.value = hit.rawValue;
              format.value = names[hit.format];
              say(box, "Scanned: " + words[names[hit.format]] + ".");
              stop();
              return;
            }
            timer = window.setTimeout(look, 300);
          }, function () { timer = window.setTimeout(look, 300); });
        }).catch(function (e) {
          stop();
          say(box, e && e.name === "NotAllowedError" ? "The camera was not allowed. Type the code instead." : "The camera could not start. Type the code instead.");
        });
      });
      q(box, "[data-stop]").addEventListener("click", function () { stop(); say(box, ""); });
    }, function () { /* typed */ });
  }

  var boxes = document.querySelectorAll("[data-device]");
  for (var i = 0; i < boxes.length; i++) {
    var kind = boxes[i].getAttribute("data-device");
    if (kind === "location") place(boxes[i]);
    else if (kind === "signature") signature(boxes[i]);
    else if (kind === "scan") scan(boxes[i]);
  }
})();
