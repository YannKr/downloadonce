// SSE client for real-time updates

function connectCampaignSSE(campaignID) {
    var es = new EventSource("/campaigns/" + campaignID + "/events");

    es.addEventListener("progress", function(e) {
        var data = JSON.parse(e.data);
        var cell = document.getElementById("progress-cell-" + data.token_id);
        if (!cell) return;
        var bar = cell.querySelector(".progress-bar");
        if (bar) {
            var fill = bar.querySelector(".progress-fill");
            var text = bar.querySelector(".progress-text");
            if (fill) fill.style.width = data.progress + "%";
            if (text) text.textContent = data.progress + "%";
        } else {
            // Create progress bar if not yet shown
            cell.innerHTML = '<div class="progress-bar"><div class="progress-fill" style="width:' +
                data.progress + '%"></div><span class="progress-text">' + data.progress + '%</span></div>';
        }
    });

    es.addEventListener("token_ready", function(e) {
        var data = JSON.parse(e.data);
        // Update state badge
        var stateEl = document.getElementById("state-" + data.token_id);
        if (stateEl) {
            stateEl.innerHTML = '<span class="badge badge-green">ACTIVE</span>';
        }
        // Update progress cell
        var cell = document.getElementById("progress-cell-" + data.token_id);
        if (cell) {
            cell.innerHTML = '<span class="text-muted">Done</span>';
        }
        // Reload page to get updated link and revoke button
        window.location.reload();
    });

    es.onerror = function() {
        // Reconnect handled automatically by EventSource
    };

    return es;
}

function connectTokenSSE(tokenID) {
    var es = new EventSource("/d/" + tokenID + "/events");

    es.addEventListener("progress", function(e) {
        var data = JSON.parse(e.data);
        var bar = document.getElementById("preparing-progress");
        if (bar) {
            var fill = bar.querySelector(".progress-fill");
            var text = bar.querySelector(".progress-text");
            if (fill) fill.style.width = data.progress + "%";
            if (text) text.textContent = data.progress + "%";
        }
        var status = document.getElementById("status-text");
        if (status) {
            if (data.progress < 30) status.textContent = "Starting...";
            else if (data.progress < 60) status.textContent = "Applying watermark...";
            else if (data.progress < 90) status.textContent = "Finalizing...";
            else status.textContent = "Almost done...";
        }
    });

    es.addEventListener("token_ready", function(e) {
        es.close();
        window.location.reload();
    });

    es.onerror = function() {
        // Reconnect handled automatically by EventSource
    };

    return es;
}

function copyLink(btn) {
    var url = btn.getAttribute("data-url");
    navigator.clipboard.writeText(url).then(function() {
        btn.textContent = "Copied!";
        setTimeout(function() { btn.textContent = "Copy"; }, 2000);
    });
}
