// The camera as a barcode scanner: fills a scan field as if typed, and submits.
// getUserMedia needs a secure context: HTTPS, or localhost in development.

(function () {
  "use strict";

  // ISBN/EAN on the back of books, and printed internal codes. The rest is
  // ignored, including the EAN_5 price barcode.
  function buildReader() {
    var hints = new Map();
    hints.set(ZXing.DecodeHintType.POSSIBLE_FORMATS, [
      ZXing.BarcodeFormat.EAN_13,
      ZXing.BarcodeFormat.EAN_8,
      ZXing.BarcodeFormat.CODE_128,
      ZXing.BarcodeFormat.CODE_39,
    ]);
    return new ZXing.BrowserMultiFormatReader(hints);
  }

  var overlay, video, message, reader, targetInput, opener;
  // Bumped on every open and close: a permission granted after Cancel belongs
  // to a session that is over, and its stream is stopped instead of shown.
  var session = 0;

  function buildOverlay() {
    overlay = document.createElement("div");
    overlay.className = "cam-overlay";
    overlay.hidden = true;
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.setAttribute("aria-label", T("js.scan_with_camera"));
    overlay.innerHTML =
      '<div class="cam-box">' +
      '  <video class="cam-video" playsinline muted autoplay></video>' +
      '  <p class="cam-message" aria-live="polite"></p>' +
      '  <button type="button" class="cam-close"></button>' +
      "</div>";
    document.body.appendChild(overlay);
    video = overlay.querySelector(".cam-video");
    message = overlay.querySelector(".cam-message");
    message.textContent = T("js.aim_barcode");
    overlay.querySelector(".cam-close").textContent = T("js.cancel");
    overlay.querySelector(".cam-close").addEventListener("click", closeCamera);
    overlay.addEventListener("click", function (e) {
      if (e.target === overlay) closeCamera();
    });
    overlay.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        e.preventDefault();
        closeCamera();
      }
    });
  }

  function stopTracks(stream) {
    if (!stream || !stream.getTracks) return;
    stream.getTracks().forEach(function (t) {
      t.stop();
    });
  }

  // Without a requested resolution iOS serves 640x480, where the bars of an
  // EAN-13 blur together and nothing decodes — the screen looks out of focus.
  // The "advanced" constraints are best effort, never blocking.
  function videoConstraints() {
    return {
      video: {
        facingMode: { ideal: "environment" },
        width: { ideal: 1920 },
        height: { ideal: 1080 },
        advanced: [{ focusMode: "continuous" }],
      },
    };
  }

  var helpTimer;

  function openCamera(input, trigger) {
    if (typeof ZXing === "undefined") {
      return; // library not loaded yet: the manual field stays usable
    }
    if (!overlay) buildOverlay();
    if (!overlay.hidden) closeCamera();
    var mine = ++session;
    targetInput = input;
    opener = trigger || null;
    overlay.hidden = false;
    overlay.querySelector(".cam-close").focus();
    message.textContent = T("js.aim_barcode");
    var current = (reader = buildReader());

    // Say what to do rather than leave a silent video running.
    clearTimeout(helpTimer);
    helpTimer = setTimeout(function () {
      if (overlay && !overlay.hidden) {
        message.textContent =
          T("js.nothing_read");
      }
    }, 8000);

    // getUserMedia is asked here rather than through decodeFromConstraints, so
    // the stream can be checked against the session before it is attached.
    Promise.resolve()
      .then(function () {
        return navigator.mediaDevices.getUserMedia(videoConstraints());
      })
      .then(function (stream) {
        if (mine !== session) {
          stopTracks(stream);
          return;
        }
        return current.decodeFromStream(stream, video, onFrame);
      })
      .catch(function (err) {
        if (mine !== session) return;
        clearTimeout(helpTimer);
        message.textContent =
          T("js.camera_unavailable", err && err.name ? err.name : "?");
      });

    function onFrame(result, err) {
      if (mine !== session) return;
      if (result) {
        onSuccess(result.getText());
        return;
      }
      // A zxing exception is a frame that held no readable code, which is
      // normal; the minified build renames its classes, so test the type.
      if (err && !(err instanceof ZXing.Exception)) {
        message.textContent = T("js.read_failed", err.name || "?");
      }
    }
  }

  // requestSubmit throws in Safari before 16, and form.submit() fires no submit
  // event, so HTMX would miss it: emit the event, submit natively if nobody took it.
  function submitForm(form) {
    if (typeof form.requestSubmit === "function") {
      form.requestSubmit();
      return;
    }
    var ev = new Event("submit", { bubbles: true, cancelable: true });
    if (form.dispatchEvent(ev)) {
      form.submit();
    }
  }

  function onSuccess(text) {
    if (navigator.vibrate) navigator.vibrate(60);
    closeCamera();
    if (!targetInput) return;
    targetInput.focus();
    targetInput.value = text.trim();
    if (targetInput.form) {
      submitForm(targetInput.form);
    }
  }

  function closeCamera() {
    session++;
    clearTimeout(helpTimer);
    if (reader) {
      try {
        reader.reset();
      } catch (e) {
        /* nothing to do */
      }
      reader = null;
    }
    // reset() stops only video tracks, and only a stream it was handed.
    if (video && video.srcObject) {
      stopTracks(video.srcObject);
      video.srcObject = null;
    }
    if (overlay && !overlay.hidden) {
      overlay.hidden = true;
      if (opener && document.contains(opener)) opener.focus();
    }
    opener = null;
  }

  function equipFields() {
    var fields = document.querySelectorAll(".scan");
    for (var i = 0; i < fields.length; i++) {
      var field = fields[i];
      if (field.dataset.camEquipped) continue;
      field.dataset.camEquipped = "1";

      var button = document.createElement("button");
      button.type = "button";
      button.className = "btn-camera";
      button.setAttribute("aria-label", T("js.scan_with_camera"));
      button.innerHTML =
        '<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" ' +
        'stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
        '<path d="M4.5 7H7l1.3-1.7a1 1 0 0 1 .8-.4h5.8a1 1 0 0 1 .8.4L17 7h2.5A1.5 1.5 0 0 1 21 8.5' +
        'v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5v-9A1.5 1.5 0 0 1 4.5 7Z"/>' +
        '<circle cx="12" cy="13" r="3.2"/></svg>';
      (function (input) {
        button.addEventListener("click", function () {
          openCamera(input, button);
        });
      })(field);

      var row = document.createElement("div");
      row.className = "scan-row";
      field.parentNode.insertBefore(row, field);
      row.appendChild(field);
      row.appendChild(button);
    }
  }

  document.addEventListener("DOMContentLoaded", equipFields);
  document.addEventListener("htmx:afterSwap", equipFields);
  // A hidden tab or a page put in the back-forward cache keeps the camera (and
  // its light) on otherwise.
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) closeCamera();
  });
  window.addEventListener("pagehide", closeCamera);
})();
