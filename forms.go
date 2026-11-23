package main

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func customerLabel(c Customer) string {
	return c.FirstName + " " + c.LastName
}

func motorcycleLabel(m Motorcycle) string {
	return "Triumph " + m.Model + " · " + m.VIN
}

func statusLabel(status string) string {
	return map[string]string{"draft": "Projekt", "signed": "Podpisana", "cancelled": "Anulowana"}[status]
}

func ids[T any](records map[int64]T) []int64 {
	result := make([]int64, 0, len(records))
	for id := range records {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func recordValues(s state, kind string, id int64) url.Values {
	v := url.Values{}
	v.Set("country", "Polska")
	v.Set("year", strconv.Itoa(warsawNow().Year()))
	v.Set("price", "0.00")
	v.Set("deposit", "0.00")
	v.Set("date", warsawNow().Format("2006-01-02"))
	v.Set("status", "draft")
	switch kind {
	case "customers":
		if c, ok := s.Customers[id]; ok {
			for name, value := range map[string]string{
				"first_name": c.FirstName, "last_name": c.LastName, "email": c.Email, "phone": c.Phone,
				"street": c.Street, "postal_code": c.PostalCode, "city": c.City, "country": c.Country,
			} {
				v.Set(name, value)
			}
		}
	case "motorcycles":
		if m, ok := s.Motorcycles[id]; ok {
			v.Set("model", m.Model)
			v.Set("vin", m.VIN)
			v.Set("color", m.Color)
			v.Set("year", strconv.Itoa(m.Year))
			v.Set("price", moneyInput(m.Price))
		}
	case "owned":
		if b, ok := s.Owned[id]; ok {
			v.Set("customer_id", strconv.FormatInt(b.CustomerID, 10))
			v.Set("make", b.Make)
			v.Set("model", b.Model)
			v.Set("vin", b.VIN)
			v.Set("year", strconv.Itoa(b.Year))
			v.Set("registration", b.Registration)
		}
	case "contracts":
		if c, ok := s.Contracts[id]; ok {
			v.Set("number", c.Number)
			v.Set("customer_id", strconv.FormatInt(c.CustomerID, 10))
			v.Set("motorcycle_id", strconv.FormatInt(c.MotorcycleID, 10))
			v.Set("date", c.Date)
			v.Set("status", c.Status)
			v.Set("price", moneyInput(c.Price))
			v.Set("deposit", moneyInput(c.Deposit))
		}
	}
	return v
}

func fieldsFor(s state, kind string, values url.Values) []field {
	fields := []field{}
	add := func(name, label, typ, hint string, required bool) {
		fields = append(fields, field{Name: name, Label: label, Type: typ, Value: values.Get(name), Hint: hint, Required: required})
	}
	customers := func() {
		add("customer_id", "Klient", "select", "", true)
		f := &fields[len(fields)-1]
		f.Options = append(f.Options, option{Value: "", Label: "Wybierz klienta"})
		for _, id := range ids(s.Customers) {
			value := strconv.FormatInt(id, 10)
			f.Options = append(f.Options, option{Value: value, Label: customerLabel(s.Customers[id]), Selected: value == f.Value})
		}
	}
	switch kind {
	case "customers":
		add("first_name", "Imię", "text", "", true)
		add("last_name", "Nazwisko", "text", "", true)
		add("email", "E-mail", "email", "", false)
		add("phone", "Telefon", "tel", "Z numerem kierunkowym kraju, np. +48.", false)
		add("street", "Ulica i numer", "text", "", false)
		add("postal_code", "Kod pocztowy", "text", "", false)
		add("city", "Miasto", "text", "", false)
		add("country", "Kraj", "text", "", true)
	case "motorcycles":
		add("model", "Model Triumph", "text", "Np. Bonneville T120.", true)
		add("vin", "VIN", "text", "17 znaków; bez liter I, O i Q. Unikalny w magazynie.", true)
		add("year", "Rok modelowy", "number", "Od 1900 do przyszłego roku.", true)
		add("color", "Kolor", "text", "", true)
		add("price", "Cena katalogowa (PLN)", "text", "Kwota w złotych, np. 54900,00. Maksymalnie 2 miejsca dziesiętne.", true)
	case "owned":
		customers()
		add("make", "Marka", "text", "Dowolna marka, nie tylko Triumph.", true)
		add("model", "Model", "text", "", true)
		add("year", "Rok", "number", "Od 1900 do przyszłego roku.", true)
		add("vin", "VIN", "text", "Opcjonalny; 17 znaków, bez liter I, O i Q.", false)
		add("registration", "Numer rejestracyjny", "text", "", false)
	case "contracts":
		add("number", "Numer umowy", "text", "Unikalny, np. TP/2026/001.", true)
		customers()
		add("motorcycle_id", "Motocykl z magazynu", "select", "Każdy motocykl może mieć tylko jedną aktywną umowę.", true)
		f := &fields[len(fields)-1]
		f.Options = append(f.Options, option{Value: "", Label: "Wybierz motocykl"})
		for _, id := range ids(s.Motorcycles) {
			value := strconv.FormatInt(id, 10)
			f.Options = append(f.Options, option{Value: value, Label: motorcycleLabel(s.Motorcycles[id]), Selected: value == f.Value})
		}
		add("date", "Data umowy", "date", "", true)
		add("status", "Status", "select", "Projekt i podpisana blokują kolejną umowę. Anulowana umożliwia ponowną sprzedaż.", true)
		f = &fields[len(fields)-1]
		for _, status := range []string{"draft", "signed", "cancelled"} {
			f.Options = append(f.Options, option{Value: status, Label: statusLabel(status), Selected: status == f.Value})
		}
		add("price", "Cena sprzedaży (PLN)", "text", "Kwota w złotych, maksymalnie 2 miejsca dziesiętne.", true)
		add("deposit", "Zaliczka (PLN)", "text", "Nie może być większa niż cena sprzedaży.", true)
	}
	return fields
}

func applyValues(s *state, kind string, id int64, values url.Values) (int64, error) {
	if id != 0 && !exists(*s, kind, id) {
		return 0, errors.New("Rekord już nie istnieje.")
	}
	isNew := id == 0
	if isNew {
		id = s.NextID
		s.NextID++
	}
	value := func(name string) string { return strings.TrimSpace(values.Get(name)) }
	number := func(name string) (int64, error) {
		n, err := strconv.ParseInt(value(name), 10, 64)
		if err != nil || n < 1 {
			return 0, errors.New("Wybierz istniejącego klienta i motocykl.")
		}
		return n, nil
	}
	modelYear := func() (int, error) {
		year, err := strconv.Atoi(value("year"))
		if err != nil || !validYear(year) {
			return 0, errors.New("Rok musi być liczbą od 1900 do przyszłego roku.")
		}
		return year, nil
	}
	switch kind {
	case "customers":
		c := s.Customers[id]
		s.Customers[id] = Customer{Audit: audit(c.Audit), ID: id, FirstName: value("first_name"), LastName: value("last_name"),
			Email: value("email"), Phone: value("phone"), Street: value("street"), PostalCode: value("postal_code"),
			City: value("city"), Country: value("country")}
	case "motorcycles":
		year, err := modelYear()
		if err != nil {
			return 0, err
		}
		price, err := parseMoney(value("price"))
		if err != nil {
			return 0, err
		}
		m := s.Motorcycles[id]
		s.Motorcycles[id] = Motorcycle{Audit: audit(m.Audit), ID: id, Model: value("model"), VIN: strings.ToUpper(value("vin")),
			Color: value("color"), Year: year, Price: price}
	case "owned":
		customer, err := number("customer_id")
		if err != nil {
			return 0, err
		}
		year, err := modelYear()
		if err != nil {
			return 0, err
		}
		b := s.Owned[id]
		s.Owned[id] = CustomerMotorcycle{Audit: audit(b.Audit), ID: id, CustomerID: customer, Make: value("make"),
			Model: value("model"), VIN: strings.ToUpper(value("vin")), Year: year, Registration: value("registration")}
	case "contracts":
		customer, err := number("customer_id")
		if err != nil {
			return 0, err
		}
		motorcycle, err := number("motorcycle_id")
		if err != nil {
			return 0, err
		}
		price, err := parseMoney(value("price"))
		if err != nil {
			return 0, err
		}
		deposit, err := parseMoney(value("deposit"))
		if err != nil {
			return 0, err
		}
		c := s.Contracts[id]
		s.Contracts[id] = SalesContract{Audit: audit(c.Audit), ID: id, Number: value("number"), Date: value("date"),
			Status: value("status"), CustomerID: customer, MotorcycleID: motorcycle, Price: price, Deposit: deposit}
	default:
		return 0, errors.New("Nieznany rodzaj rekordu.")
	}
	return id, nil
}

func (a *App) form(w http.ResponseWriter, r *http.Request, kind string, id int64, token string) {
	s := a.store.snapshot()
	values := recordValues(s, kind, id)
	if id == 0 {
		if customer := r.URL.Query().Get("customer_id"); customer != "" {
			values.Set("customer_id", customer)
		}
		if motorcycle := r.URL.Query().Get("motorcycle_id"); motorcycle != "" {
			values.Set("motorcycle_id", motorcycle)
			if m, ok := s.Motorcycles[parseID(motorcycle)]; ok {
				values.Set("price", moneyInput(m.Price))
			}
		}
	}
	p := page{Title: "Nowy rekord", Subtitle: titles[kind], Kind: kind, CSRF: token, Action: r.URL.Path}
	status := http.StatusOK
	if id > 0 {
		p.Title, p.RecordURL = "Edytuj rekord", pathFor(kind, id)
	}
	if r.Method == http.MethodPost {
		values = r.PostForm
		var savedID int64
		err := a.store.update(func(next *state) error {
			var err error
			savedID, err = applyValues(next, kind, id, values)
			return err
		})
		if err == nil {
			http.Redirect(w, r, pathFor(kind, savedID)+"?saved=1", http.StatusSeeOther)
			return
		}
		p.Error, status = err.Error(), http.StatusUnprocessableEntity
	}
	p.Fields = fieldsFor(a.store.snapshot(), kind, values)
	if (kind == "owned" || kind == "contracts") && len(s.Customers) == 0 {
		p.Subtitle += " · Najpierw dodaj klienta."
	}
	if kind == "contracts" && len(s.Motorcycles) == 0 {
		p.Subtitle += " · Najpierw dodaj motocykl do magazynu."
	}
	a.render(w, r, "form", p, status)
}

func parseID(value string) int64 {
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}
