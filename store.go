package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Customer struct {
	Audit
	ID                       int64
	FirstName, LastName      string
	Email, Phone             string
	Street, PostalCode, City string
	Country                  string
}

type Motorcycle struct {
	Audit
	ID                int64
	Model, VIN, Color string
	Year              int
	Price             int64 // Integer grosz; never floating-point currency.
}

type CustomerMotorcycle struct {
	Audit
	ID               int64
	CustomerID       int64
	Make, Model, VIN string
	Year             int
	Registration     string
}

type SalesContract struct {
	Audit
	ID                       int64
	Number, Date, Status     string
	CustomerID, MotorcycleID int64
	Price, Deposit           int64
}

type Audit struct {
	CreatedAt, UpdatedAt string
}

func audit(previous Audit) Audit {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if previous.CreatedAt == "" {
		previous.CreatedAt = now
	}
	previous.UpdatedAt = now
	return previous
}

type state struct {
	Version     int
	NextID      int64
	Customers   map[int64]Customer
	Motorcycles map[int64]Motorcycle
	Owned       map[int64]CustomerMotorcycle
	Contracts   map[int64]SalesContract
}

func emptyState() state {
	return state{Version: 1, NextID: 1, Customers: map[int64]Customer{},
		Motorcycles: map[int64]Motorcycle{}, Owned: map[int64]CustomerMotorcycle{},
		Contracts: map[int64]SalesContract{}}
}

func (s state) clone() state {
	s.Customers = maps.Clone(s.Customers)
	s.Motorcycles = maps.Clone(s.Motorcycles)
	s.Owned = maps.Clone(s.Owned)
	s.Contracts = maps.Clone(s.Contracts)
	return s
}

var vinPattern = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)
var moneyPattern = regexp.MustCompile(`^[0-9]{1,10}([.,][0-9]{1,2})?$`)

func parseMoney(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if !moneyPattern.MatchString(value) {
		return 0, errors.New("Kwota musi być nieujemna, z maksymalnie dwoma miejscami po przecinku.")
	}
	parts := strings.Split(strings.ReplaceAll(value, ",", "."), ".")
	whole, _ := strconv.ParseInt(parts[0], 10, 64)
	fraction := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 1 {
			parts[1] += "0"
		}
		fraction, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	return whole*100 + fraction, nil
}

func moneyInput(amount int64) string {
	return fmt.Sprintf("%d.%02d", amount/100, amount%100)
}

func money(amount int64) string {
	whole := strconv.FormatInt(amount/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + " " + whole[i:]
	}
	return fmt.Sprintf("%s,%02d zł", whole, amount%100)
}

func required(values ...string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}

func bounded(values ...string) bool {
	for _, v := range values {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 250 || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	return true
}

func validYear(year int) bool {
	return year >= 1900 && year <= warsawNow().Year()+1
}

func validAmount(amount int64) bool {
	return amount >= 0 && amount <= 999999999999
}

func (s state) validate() error {
	if s.Version != 1 || s.NextID < 1 || s.Customers == nil || s.Motorcycles == nil || s.Owned == nil || s.Contracts == nil {
		return errors.New("Nieprawidłowy format magazynu danych.")
	}
	ids := map[int64]bool{}
	checkID := func(key, id int64) bool {
		if key < 1 || id != key || id >= s.NextID || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	for key, c := range s.Customers {
		if !checkID(key, c.ID) || !required(c.FirstName, c.LastName, c.Country) ||
			!bounded(c.FirstName, c.LastName, c.Email, c.Phone, c.Street, c.PostalCode, c.City, c.Country) {
			return errors.New("Klient: podaj imię, nazwisko i kraj. Pola mogą mieć maksymalnie 250 znaków.")
		}
		if c.Email != "" {
			address, err := mail.ParseAddress(c.Email)
			if err != nil || address.Address != c.Email {
				return errors.New("Klient: podaj prawidłowy adres e-mail.")
			}
		}
	}
	vins := map[string]bool{}
	for key, m := range s.Motorcycles {
		if !checkID(key, m.ID) || !required(m.Model, m.Color) || !bounded(m.Model, m.Color) ||
			!validYear(m.Year) || !vinPattern.MatchString(m.VIN) || !validAmount(m.Price) {
			return errors.New("Motocykl: wymagane model i kolor, rok od 1900 do przyszłego roku, VIN (17 znaków, bez I/O/Q) oraz nieujemna cena.")
		}
		if vins[m.VIN] {
			return errors.New("Motocykl z tym numerem VIN już istnieje w magazynie.")
		}
		vins[m.VIN] = true
	}
	for key, b := range s.Owned {
		if !checkID(key, b.ID) || !required(b.Make, b.Model) || !bounded(b.Make, b.Model, b.Registration) ||
			!validYear(b.Year) || (b.VIN != "" && !vinPattern.MatchString(b.VIN)) {
			return errors.New("Motocykl klienta: wymagane marka, model i prawidłowy rok; VIN jest opcjonalny (17 znaków, bez I/O/Q).")
		}
		if _, ok := s.Customers[b.CustomerID]; !ok {
			return errors.New("Motocykl klienta wymaga istniejącego klienta. Nie można usunąć powiązanego klienta.")
		}
	}
	numbers := map[string]bool{}
	active := map[int64]bool{}
	for key, c := range s.Contracts {
		date, err := time.Parse("2006-01-02", c.Date)
		if !checkID(key, c.ID) || !required(c.Number) || !bounded(c.Number) ||
			err != nil || date.Year() < 1900 || date.Year() > 9999 ||
			!validAmount(c.Price) || !validAmount(c.Deposit) || c.Deposit > c.Price ||
			(c.Status != "draft" && c.Status != "signed" && c.Status != "cancelled") {
			return errors.New("Umowa: wymagane numer, prawidłowa data i status; kwoty nieujemne, zaliczka nie może przekroczyć ceny.")
		}
		if _, ok := s.Customers[c.CustomerID]; !ok {
			return errors.New("Umowa wymaga istniejącego klienta. Nie można usunąć klienta powiązanego z umową.")
		}
		if _, ok := s.Motorcycles[c.MotorcycleID]; !ok {
			return errors.New("Umowa wymaga istniejącego motocykla. Nie można usunąć motocykla powiązanego z umową.")
		}
		number := strings.ToUpper(c.Number)
		if numbers[number] {
			return errors.New("Numer umowy musi być unikalny.")
		}
		numbers[number] = true
		if c.Status != "cancelled" {
			if active[c.MotorcycleID] {
				return errors.New("Ten motocykl ma już aktywną umowę (projekt lub podpisana). Najpierw anuluj poprzednią umowę.")
			}
			active[c.MotorcycleID] = true
		}
	}
	return nil
}

type Store struct {
	mu   sync.RWMutex
	path string
	data state
}

func openStore(path string) (*Store, error) {
	store := &Store{path: path, data: emptyState()}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := persist(path, store.data); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	store.data = state{}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.data); err != nil {
		return nil, fmt.Errorf("read CRM data: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("CRM data contains trailing content")
	}
	if err := store.data.validate(); err != nil {
		return nil, fmt.Errorf("invalid CRM data: %w", err)
	}
	return store, nil
}

func persist(path string, data state) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".crm-write-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := json.NewEncoder(file).Encode(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	// The rename commits the transaction. Directory sync is best-effort on
	// platforms/filesystems that do not support syncing directory handles.
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func (s *Store) snapshot() state {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.clone()
}

func (s *Store) update(change func(*state) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data.clone()
	if err := change(&next); err != nil {
		return err
	}
	if err := next.validate(); err != nil {
		return err
	}
	if err := persist(s.path, next); err != nil {
		return errors.New("Nie udało się zapisać danych. Żadne zmiany nie zostały zastosowane.")
	}
	s.data = next
	return nil
}
