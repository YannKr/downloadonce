// upload.js -- Chunked file uploader for DownloadOnce
// Exposes window.ChunkedUpload.start(file, opts)
(function(global) {
  "use strict";

  var CHUNK_SIZE = 5 * 1024 * 1024; // 5 MB

  function getCsrfToken() {
    var el = document.querySelector("meta[name=csrf-token]");
    return el ? el.getAttribute("content") : "";
  }

  function jsonFetch(method, url, body, headers) {
    var opts = {
      method: method,
      headers: Object.assign({ "Content-Type": "application/json" }, headers || {})
    };
    if (body !== undefined) opts.body = JSON.stringify(body);
    return fetch(url, opts).then(function(res) {
      return res.json().then(function(data) {
        if (!res.ok) throw new Error(data.error || "HTTP " + res.status);
        return data;
      });
    });
  }

  function uploadChunk(sessionId, index, blob) {
    return fetch("/upload/chunks/" + sessionId + "/" + index, {
      method: "PUT",
      headers: { "X-CSRF-Token": getCsrfToken() },
      body: blob
    }).then(function(res) {
      return res.json().then(function(data) {
        if (!res.ok) throw new Error(data.error || "HTTP " + res.status);
        return data;
      });
    });
  }

  function start(file, opts) {
    opts = opts || {};
    var cancelled = false;
    var sessionId = null;

    function cancel() {
      cancelled = true;
      if (sessionId) {
        fetch("/upload/chunks/" + sessionId, {
          method: "DELETE",
          headers: { "X-CSRF-Token": getCsrfToken() }
        }).catch(function() {});
      }
      if (opts.onCancel) opts.onCancel();
    }

    if (opts.getCancelHandle) opts.getCancelHandle(cancel);

    var token = getCsrfToken();
    var mimeType = file.type || "application/octet-stream";

    if (opts.onProgress) opts.onProgress(0, "Initialising...");

    jsonFetch("POST", "/upload/chunks/init", {
      filename:   file.name,
      size:       file.size,
      mime_type:  mimeType,
      chunk_size: CHUNK_SIZE
    }, { "X-CSRF-Token": token })
    .then(function(data) {
      if (cancelled) return;
      sessionId = data.session_id;
      var chunkCount = data.total_chunks;

      var idx = 0;
      function nextChunk() {
        if (cancelled) return;
        if (idx >= chunkCount) {
          if (opts.onProgress) opts.onProgress(99, "Finalising...");
          return jsonFetch("POST", "/upload/chunks/" + sessionId + "/complete", {}, {
            "X-CSRF-Token": getCsrfToken()
          }).then(function(result) {
            if (opts.onProgress) opts.onProgress(100, "Done");
            if (opts.onComplete) opts.onComplete(result.asset_id);
          });
        }
        var start = idx * CHUNK_SIZE;
        var end   = Math.min(start + CHUNK_SIZE, file.size);
        var blob  = file.slice(start, end);
        return uploadChunk(sessionId, idx, blob).then(function() {
          idx++;
          var pct = Math.round((idx / chunkCount) * 98);
          if (opts.onProgress) opts.onProgress(pct, "Uploading... " + idx + "/" + chunkCount + " chunks");
          return nextChunk();
        });
      }
      return nextChunk();
    })
    .catch(function(err) {
      if (!cancelled && opts.onError) opts.onError(err.message || String(err));
    });
  }

  global.ChunkedUpload = { start: start };
})(window);
