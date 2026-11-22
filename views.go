package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func availability(s state, id int64) string {
	for _, c := range s.Contracts {
		if c.MotorcycleID == id && c.Status != "cancelled" {
			return c.Status
		}
	}
	return "available"
}

func availabilityLabel(status string) string {
	return map[string]string{"available": "Dostępny", "draft": "Zarezerwowany", "signed": "Sprzedany"}[status]
}

func tableRows(s state, kind string, include func(int64) bool) ([]string, []row) {
	rows := []row{}
	headers := []string{}
	add := func(id int64, cells ...cell) {
		if include == nil || include(id) {
			rows = append(rows, row{Cells: cells})
		}
	}
	switch kind {
	case "customers":
		headers = []string{"Klient", "Kontakt", "Miasto", "Kraj"}
		for _, id := range ids(s.Customers) {
			c := s.Customers[id]
			add(id, cell{customerLabel(c), pathFor(kind, id)}, cell{strings.TrimSpace(c.Email + " " + c.Phone), ""},
				cell{c.City, ""}, cell{c.Country, ""})
		}
	case "motorcycles":
		headers = []string{"Model Triumph", "Rok / kolor", "VIN", "Cena", "Dostępność"}
		for _, id := range ids(s.Motorcycles) {
			m := s.Motorcycles[id]
			add(id, cell{m.Model, pathFor(kind, id)}, cell{fmt.Sprintf("%d · %s", m.Year, m.Color), ""},
				cell{m.VIN, ""}, cell{money(m.Price), ""}, cell{availabilityLabel(availability(s, id)), ""})
		}
	case "owned":
		headers = []string{"Motocykl", "Klient", "Rok", "Rejestracja / VIN"}
		for _, id := range ids(s.Owned) {
			b := s.Owned[id]
			add(id, cell{b.Make + " " + b.Model, pathFor(kind, id)}, cell{customerLabel(s.Customers[b.CustomerID]), pathFor("customers", b.CustomerID)},
				cell{strconv.Itoa(b.Year), ""}, cell{strings.TrimSpace(b.Registration + " " + b.VIN), ""})
		}
	case "contracts":
		headers = []string{"Numer / data", "Klient", "Motocykl", "Cena / zaliczka", "Status"}
		for _, id := range ids(s.Contracts) {
			c := s.Contracts[id]
			add(id, cell{c.Number + " · " + c.Date, pathFor(kind, id)}, cell{customerLabel(s.Customers[c.CustomerID]), pathFor("customers", c.CustomerID)},
				cell{motorcycleLabel(s.Motorcycles[c.MotorcycleID]), pathFor("motorcycles", c.MotorcycleID)},
				cell{money(c.Price) + " / " + money(c.Deposit), ""}, cell{statusLabel(c.Status), ""})
		}
	}
	return headers, rows
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request, token string) {
	s := a.store.snapshot()
	available, active := 0, 0
	for id := range s.Motorcycles {
		if availability(s, id) == "available" {
			available++
		}
	}
	for _, c := range s.Contracts {
		if c.Status != "cancelled" {
			active++
		}
	}
	p := page{Title: "Dobry dzień zaczyna się od Triumph.", Subtitle: "Twój salon. Twoi klienci. Wszystko w jednym miejscu.", CSRF: token}
	p.Counts = []detail{
		{"Klienci", strconv.Itoa(len(s.Customers)), "/customers"},
		{"Motocykle w magazynie", strconv.Itoa(len(s.Motorcycles)), "/motorcycles"},
		{"Motocykle klientów", strconv.Itoa(len(s.Owned)), "/owned"},
		{"Umowy sprzedaży", strconv.Itoa(len(s.Contracts)), "/contracts"},
		{"Dostępne motocykle", strconv.Itoa(available), "/motorcycles?status=available"},
		{"Aktywne umowy", strconv.Itoa(active), "/contracts?status=active"},
	}
	headers, rows := tableRows(s, "contracts", nil)
	if len(rows) > 5 {
		rows = rows[len(rows)-5:]
	}
	p.Sections = []section{{Title: "Ostatnio dodane umowy", AddURL: "/contracts/new", Headers: headers, Rows: rows}}
	a.render(w, r, "dashboard", p, http.StatusOK)
}

func (a *App) list(w http.ResponseWriter, r *http.Request, kind, token string) {
	s := a.store.snapshot()
	p := page{Title: titles[kind], Subtitle: "Przeglądaj, wyszukuj i zarządzaj danymi salonu.", Kind: kind, CSRF: token, Query: r.URL.Query().Get("q")}
	p.Headers, p.Rows = tableRows(s, kind, nil)
	filter := r.URL.Query().Get("status")
	if kind == "contracts" || kind == "motorcycles" {
		p.Filters = []option{{Value: "", Label: "Wszystkie statusy", Selected: filter == ""}}
		if kind == "contracts" {
			for _, status := range []string{"active", "draft", "signed", "cancelled"} {
				label := statusLabel(status)
				if status == "active" {
					label = "Aktywne (projekt i podpisana)"
				}
				p.Filters = append(p.Filters, option{Value: status, Label: label, Selected: filter == status})
			}
		} else {
			for _, status := range []string{"available", "draft", "signed"} {
				p.Filters = append(p.Filters, option{Value: status, Label: availabilityLabel(status), Selected: filter == status})
			}
		}
		p.Headers, p.Rows = tableRows(s, kind, func(id int64) bool {
			if filter == "" {
				return true
			}
			if kind == "motorcycles" {
				return availability(s, id) == filter
			}
			c := s.Contracts[id]
			return c.Status == filter || (filter == "active" && c.Status != "cancelled")
		})
	}
	query := strings.ToLower(strings.TrimSpace(p.Query))
	matching := []row{}
	for _, row := range p.Rows {
		text := ""
		for _, cell := range row.Cells {
			text += " " + cell.Text
		}
		if strings.Contains(strings.ToLower(text), query) {
			matching = append(matching, row)
		}
	}
	p.Rows = matching
	p.Subtitle = fmt.Sprintf("%d rekordów · Dane kontaktowe i finansowe dostępne tylko po zalogowaniu.", len(p.Rows))
	a.render(w, r, "list", p, http.StatusOK)
}

func auditDetails(value Audit) []detail {
	format := func(raw string) string {
		date, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return "—"
		}
		location, _ := time.LoadLocation("Europe/Warsaw")
		return date.In(location).Format("02.01.2006 15:04:05")
	}
	return []detail{{Label: "Utworzono (Warszawa)", Value: format(value.CreatedAt)},
		{Label: "Zaktualizowano (Warszawa)", Value: format(value.UpdatedAt)}}
}

func (a *App) show(w http.ResponseWriter, r *http.Request, kind string, id int64, token, problem string, status int) {
	s := a.store.snapshot()
	if !exists(s, kind, id) {
		http.NotFound(w, r)
		return
	}
	p := page{Kind: kind, Subtitle: titles[kind], CSRF: token, RecordURL: pathFor(kind, id), CanDelete: true, Error: problem}
	add := func(label, value, link string) { p.Details = append(p.Details, detail{label, value, link}) }
	related := func(title, addURL, relatedKind string, include func(int64) bool) {
		headers, rows := tableRows(s, relatedKind, include)
		p.Sections = append(p.Sections, section{Title: title, AddURL: addURL, Headers: headers, Rows: rows})
	}
	switch kind {
	case "customers":
		c := s.Customers[id]
		p.Title = customerLabel(c)
		add("E-mail", c.Email, "")
		add("Telefon", c.Phone, "")
		add("Ulica i numer", c.Street, "")
		add("Kod pocztowy", c.PostalCode, "")
		add("Miasto", c.City, "")
		add("Kraj", c.Country, "")
		p.Details = append(p.Details, auditDetails(c.Audit)...)
		related("Motocykle klienta", fmt.Sprintf("/owned/new?customer_id=%d", id), "owned", func(b int64) bool { return s.Owned[b].CustomerID == id })
		related("Umowy klienta", fmt.Sprintf("/contracts/new?customer_id=%d", id), "contracts", func(c int64) bool { return s.Contracts[c].CustomerID == id })
	case "motorcycles":
		m := s.Motorcycles[id]
		p.Title = "Triumph " + m.Model
		add("VIN", m.VIN, "")
		add("Rok modelowy", strconv.Itoa(m.Year), "")
		add("Kolor", m.Color, "")
		add("Cena katalogowa", money(m.Price), "")
		add("Dostępność", availabilityLabel(availability(s, id)), "")
		p.Details = append(p.Details, auditDetails(m.Audit)...)
		related("Powiązane umowy", fmt.Sprintf("/contracts/new?motorcycle_id=%d", id), "contracts", func(c int64) bool { return s.Contracts[c].MotorcycleID == id })
	case "owned":
		b := s.Owned[id]
		p.Title = b.Make + " " + b.Model
		add("Klient", customerLabel(s.Customers[b.CustomerID]), pathFor("customers", b.CustomerID))
		add("Rok", strconv.Itoa(b.Year), "")
		add("VIN", b.VIN, "")
		add("Numer rejestracyjny", b.Registration, "")
		p.Details = append(p.Details, auditDetails(b.Audit)...)
	case "contracts":
		c := s.Contracts[id]
		p.Title = "Umowa " + c.Number
		add("Status", statusLabel(c.Status), "")
		add("Data umowy", c.Date, "")
		add("Klient", customerLabel(s.Customers[c.CustomerID]), pathFor("customers", c.CustomerID))
		add("Motocykl", motorcycleLabel(s.Motorcycles[c.MotorcycleID]), pathFor("motorcycles", c.MotorcycleID))
		add("Cena sprzedaży", money(c.Price), "")
		add("Zaliczka", money(c.Deposit), "")
		add("Pozostało do zapłaty", money(c.Price-c.Deposit), "")
		p.Details = append(p.Details, auditDetails(c.Audit)...)
	}
	a.render(w, r, "detail", p, status)
}
