package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type storagePageData struct {
	TotalBytes       uint64
	FreeBytes        uint64
	AppBytes         uint64
	WatermarkedBytes uint64
	AssetsBytes      uint64
	UploadsBytes     uint64
	PctFree          float64
	PctUsed          float64
	WarnLevel        int
	WarnMsg          string
	CapturedAt       string
}

func (h *Handler) AdminStorage(w http.ResponseWriter, r *http.Request) {
	if h.DiskCache == nil {
		http.Error(w, "Disk monitoring not available", 503)
		return
	}
	stats := h.DiskCache.Get()
	warnLevel := stats.WarningLevel(h.Cfg.DiskWarnYellowPct, h.Cfg.DiskWarnRedPct, h.Cfg.DiskWarnBlockPct)
	pctFree := stats.PctFree()
	h.renderAuth(w, r, "admin_storage.html", "Storage", storagePageData{
		TotalBytes:       stats.TotalBytes,
		FreeBytes:        stats.FreeBytes,
		AppBytes:         stats.AppBytes,
		WatermarkedBytes: stats.WatermarkedBytes,
		AssetsBytes:      stats.AssetsBytes,
		UploadsBytes:     stats.UploadsBytes,
		PctFree:          pctFree,
		PctUsed:          100 - pctFree,
		WarnLevel:        warnLevel,
		WarnMsg:          diskWarnMsg(warnLevel, pctFree),
		CapturedAt:       stats.CapturedAt.Format("2006-01-02 15:04:05 UTC"),
	})
}

func (h *Handler) AdminStorageJSON(w http.ResponseWriter, r *http.Request) {
	if h.DiskCache == nil {
		http.Error(w, `{"error":"disk monitoring not available"}`, 503)
		return
	}
	stats := h.DiskCache.Get()
	warnLevel := stats.WarningLevel(h.Cfg.DiskWarnYellowPct, h.Cfg.DiskWarnRedPct, h.Cfg.DiskWarnBlockPct)
	warnStr := []string{"none", "yellow", "red", "block"}[warnLevel]
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_bytes":       stats.TotalBytes,
		"free_bytes":        stats.FreeBytes,
		"app_bytes":         stats.AppBytes,
		"watermarked_bytes": stats.WatermarkedBytes,
		"assets_bytes":      stats.AssetsBytes,
		"uploads_bytes":     stats.UploadsBytes,
		"pct_free":          stats.PctFree(),
		"warning":           warnStr,
		"captured_at":       stats.CapturedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func diskWarnMsg(level int, pctFree float64) string {
	switch level {
	case 1:
		return fmt.Sprintf("%.1f%% free — running low", pctFree)
	case 2:
		return fmt.Sprintf("%.1f%% free — critically low", pctFree)
	case 3:
		return fmt.Sprintf("%.1f%% free — disk full, new publishes blocked", pctFree)
	default:
		return ""
	}
}
