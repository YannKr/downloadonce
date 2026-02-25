package handler

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

type groupListData struct {
	Groups []model.RecipientGroupSummary
}

type groupDetailData struct {
	Group      model.RecipientGroup
	Members    []model.RecipientGroupMember
	NonMembers []model.Recipient
}

func (h *Handler) GroupList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	groups, err := db.ListRecipientGroups(h.DB, accountID)
	if err != nil {
		slog.Error("list recipient groups", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "recipient_groups.html", "Recipient Groups", groupListData{Groups: groups})
}

func (h *Handler) GroupCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	if name == "" {
		groups, _ := db.ListRecipientGroups(h.DB, accountID)
		h.render(w, r, "recipient_groups.html", PageData{
			Title: "Recipient Groups", Authenticated: true,
			IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
			Error: "Group name is required.",
			Data:  groupListData{Groups: groups},
		})
		return
	}
	id := uuid.New().String()
	if err := db.CreateRecipientGroup(h.DB, id, accountID, name, description); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			groups, _ := db.ListRecipientGroups(h.DB, accountID)
			h.render(w, r, "recipient_groups.html", PageData{
				Title: "Recipient Groups", Authenticated: true,
				IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
				Error: "A group named '" + name + "' already exists.",
				Data:  groupListData{Groups: groups},
			})
			return
		}
		slog.Error("create recipient group", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	db.InsertAuditLog(h.DB, accountID, "group_created", "group", id, name, r.RemoteAddr)
	setFlash(w, "Group '"+name+"' created.")
	http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
}

func (h *Handler) GroupDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	members, _ := db.ListGroupMembers(h.DB, id, group.AccountID)
	nonMembers, _ := db.ListNonMembers(h.DB, id)
	h.renderAuth(w, r, "recipient_group_detail.html", group.Name, groupDetailData{
		Group:      *group,
		Members:    members,
		NonMembers: nonMembers,
	})
}

func (h *Handler) GroupEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	if name == "" {
		setFlash(w, "Group name cannot be empty.")
		http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
		return
	}
	if err := db.UpdateRecipientGroup(h.DB, id, group.AccountID, name, description); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			setFlash(w, "A group named '"+name+"' already exists.")
			http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
			return
		}
		slog.Error("update recipient group", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	db.InsertAuditLog(h.DB, accountID, "group_updated", "group", id, name, r.RemoteAddr)
	setFlash(w, "Group updated.")
	http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
}

func (h *Handler) GroupDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	if err := db.DeleteRecipientGroup(h.DB, id, group.AccountID); err != nil {
		slog.Error("delete recipient group", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	db.InsertAuditLog(h.DB, accountID, "group_deleted", "group", id, group.Name, r.RemoteAddr)
	setFlash(w, "Group '"+group.Name+"' deleted.")
	http.Redirect(w, r, "/recipients/groups", http.StatusSeeOther)
}

func (h *Handler) GroupAddMembers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	added := 0
	for _, rid := range r.Form["recipient_ids"] {
		if err := db.AddGroupMember(h.DB, id, rid); err == nil {
			added++
			db.InsertAuditLog(h.DB, accountID, "group_member_added", "group", id, rid, r.RemoteAddr)
		}
	}
	setFlash(w, fmt.Sprintf("%d member(s) added.", added))
	http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
}

func (h *Handler) GroupRemoveMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	recipientID := chi.URLParam(r, "recipientID")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	if err := db.RemoveGroupMember(h.DB, id, recipientID); err != nil {
		slog.Error("remove group member", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	db.InsertAuditLog(h.DB, accountID, "group_member_removed", "group", id, recipientID, r.RemoteAddr)
	setFlash(w, "Member removed.")
	http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
}

func (h *Handler) GroupImport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	group, err := db.GetRecipientGroupByID(h.DB, id)
	if err != nil || group == nil {
		http.NotFound(w, r)
		return
	}
	if group.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		http.NotFound(w, r)
		return
	}
	r.ParseMultipartForm(10 << 20)
	file, _, err := r.FormFile("file")
	if err != nil {
		setFlash(w, "No file uploaded.")
		http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	var added, newRecipients, alreadyMember int
	firstRow := true
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) < 2 {
			continue
		}
		name := strings.TrimSpace(record[0])
		email := strings.ToLower(strings.TrimSpace(record[1]))
		org := ""
		if len(record) >= 3 {
			org = strings.TrimSpace(record[2])
		}
		// Skip header row
		if firstRow {
			firstRow = false
			if strings.EqualFold(name, "name") && strings.EqualFold(email, "email") {
				continue
			}
		}
		if name == "" || email == "" {
			continue
		}
		recipient, _ := db.GetOrCreateRecipientByEmail(h.DB, accountID, name, email, org)
		if recipient.ID == "" {
			recipient.ID = uuid.New().String()
			if err := db.CreateRecipient(h.DB, recipient); err != nil {
				slog.Error("csv import: create recipient", "error", err)
				continue
			}
			newRecipients++
		}
		prevAdded := added
		if err := db.AddGroupMember(h.DB, id, recipient.ID); err == nil {
			added++
			db.InsertAuditLog(h.DB, accountID, "group_member_added", "group", id, recipient.ID, r.RemoteAddr)
		}
		if added == prevAdded {
			alreadyMember++
		}
	}
	parts := []string{}
	if newRecipients > 0 {
		parts = append(parts, fmt.Sprintf("%d new recipient(s) created", newRecipients))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added to group", added))
	}
	if alreadyMember > 0 {
		parts = append(parts, fmt.Sprintf("%d already member(s)", alreadyMember))
	}
	msg := "Import complete."
	if len(parts) > 0 {
		msg = strings.Join(parts, ", ") + "."
	}
	db.InsertAuditLog(h.DB, accountID, "group_import", "group", id, msg, r.RemoteAddr)
	setFlash(w, msg)
	http.Redirect(w, r, "/recipients/groups/"+id, http.StatusSeeOther)
}
