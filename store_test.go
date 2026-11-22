package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testDirectory(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := openStore(filepath.Join(testDirectory(t), "private", "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func customerForm() url.Values {
	return url.Values{"first_name": {"Anna"}, "last_name": {"Kowalska"}, "email": {"anna@example.test"},
		"phone": {"+48 500 100 200"}, "street": {"Motocyklowa 12"}, "postal_code": {"00-001"}, "city": {"Warszawa"}, "country": {"Polska"}}
}

func motorcycleForm() url.Values {
	return url.Values{"model": {"Bonneville T120"}, "vin": {"SMTD40HL5NT123456"}, "year": {"2024"}, "color": {"Jet Black"}, "price": {"54900,00"}}
}

func contractForm(customer, motorcycle int64) url.Values {
	return url.Values{"number": {"TP/2026/001"}, "date": {"2026-11-22"}, "status": {"draft"},
		"customer_id": {fmt.Sprint(customer)}, "motorcycle_id": {fmt.Sprint(motorcycle)}, "price": {"53000.00"}, "deposit": {"5000,25"}}
}

func ownedForm(customer int64) url.Values {
	return url.Values{"customer_id": {fmt.Sprint(customer)}, "make": {"Honda"}, "model": {"CB500F"},
		"year": {"2020"}, "vin": {""}, "registration": {"WA 1234"}}
}

func save(t *testing.T, store *Store, kind string, id int64, values url.Values) int64 {
	t.Helper()
	var result int64
	err := store.update(func(s *state) error {
		var err error
		result, err = applyValues(s, kind, id, values)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func seed(t *testing.T, store *Store) (int64, int64) {
	t.Helper()
	return save(t, store, "customers", 0, customerForm()), save(t, store, "motorcycles", 0, motorcycleForm())
}

func TestMoneyExact(t *testing.T) {
	for input, want := range map[string]int64{"0": 0, "0.01": 1, "12,3": 1230, "54900,99": 5490099, " 12.50 ": 1250, "9999999999.99": 999999999999} {
		got, err := parseMoney(input)
		if err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "+1", "1e3", "NaN", "1.234", "1,2.3", "1 000", ".50", "2.", "", "10000000000", "1/2", "0xff"} {
		if _, err := parseMoney(input); err == nil {
			t.Errorf("accepted malformed money %q", input)
		}
	}
	if got := money(5490099); got != "54 900,99 zł" {
		t.Errorf("formatted money %q", got)
	}
}

func TestStorePersistenceAndPermissions(t *testing.T) {
	store := testStore(t)
	customer, motorcycle := seed(t, store)
	owned := save(t, store, "owned", 0, ownedForm(customer))
	contract := save(t, store, "contracts", 0, contractForm(customer, motorcycle))
	before := store.snapshot()
	if before.Contracts[contract].Deposit != 500025 || before.Owned[owned].Make != "Honda" {
		t.Fatal("currency or existing motorcycle data lost")
	}
	for path, want := range map[string]os.FileMode{store.path: 0600, filepath.Dir(store.path): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: permissions error %v (wanted %o)", path, err, want)
		}
	}
	reopened, err := openStore(store.path)
	if err != nil || !reflect.DeepEqual(before, reopened.snapshot()) {
		t.Fatalf("restart did not preserve data: %v", err)
	}
	old := before.Customers[customer]
	if _, err := time.Parse(time.RFC3339Nano, old.CreatedAt); err != nil {
		t.Fatalf("missing audit timestamp: %v", err)
	}
	values := customerForm()
	values.Set("city", "Kraków")
	save(t, reopened, "customers", customer, values)
	updated := reopened.snapshot().Customers[customer]
	if updated.CreatedAt != old.CreatedAt || updated.UpdatedAt == old.UpdatedAt {
		t.Fatal("created/updated timestamps not preserved")
	}
}

func TestStoreFailsClosedForCorruptFiles(t *testing.T) {
	for name, content := range map[string]string{
		"empty": "", "syntax": "{broken", "incomplete": "{}", "version": `{"Version":2}`,
		"missing maps": `{"Version":1,"NextID":1}`, "trailing": `{} {}`, "unknown": `{"unexpected":true}`,
		"null": "null",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(testDirectory(t), "crm.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := openStore(path); err == nil {
				t.Fatal("corrupt/incomplete store accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != content {
				t.Fatal("corrupt data was overwritten")
			}
		})
	}
	dir := testDirectory(t)
	if _, err := openStore(dir); err == nil {
		t.Fatal("read failure accepted")
	}
}

func TestValidationPreservesState(t *testing.T) {
	tests := []struct {
		name, kind, field, value string
	}{
		{"required customer", "customers", "first_name", ""},
		{"invalid email", "customers", "email", "invalid"},
		{"long field", "customers", "last_name", strings.Repeat("x", 251)},
		{"invalid control characters", "customers", "phone", "123\n456"},
		{"negative inventory price", "motorcycles", "price", "-1"},
		{"invalid year", "motorcycles", "year", "1899"},
		{"future year", "motorcycles", "year", "9999"},
		{"32-bit wrapping inventory year", "motorcycles", "year", "4294969320"},
		{"native overflow inventory year", "motorcycles", "year", "9223372036854775808"},
		{"invalid VIN", "motorcycles", "vin", "SMTI40HL5NT123456"},
		{"short VIN", "motorcycles", "vin", "123"},
		{"empty model", "motorcycles", "model", ""},
		{"negative contract price", "contracts", "price", "-1"},
		{"negative deposit", "contracts", "deposit", "-0.01"},
		{"large deposit", "contracts", "deposit", "53000.01"},
		{"invalid date", "contracts", "date", "2026-02-30"},
		{"invalid date format", "contracts", "date", "22.11.2026"},
		{"invalid status", "contracts", "status", "sold"},
		{"missing customer reference", "contracts", "customer_id", "999"},
		{"missing bike reference", "contracts", "motorcycle_id", "999"},
		{"missing owned customer", "owned", "customer_id", "999"},
		{"invalid owned VIN", "owned", "vin", "bad"},
		{"invalid owned year", "owned", "year", "1800"},
		{"32-bit wrapping owned year", "owned", "year", "4294969320"},
		{"native overflow owned year", "owned", "year", "9223372036854775808"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			customer, motorcycle := seed(t, store)
			values := map[string]url.Values{"customers": customerForm(), "motorcycles": motorcycleForm(),
				"owned": ownedForm(customer), "contracts": contractForm(customer, motorcycle)}[tt.kind]
			// A new inventory record would also duplicate the existing VIN.
			if tt.kind == "motorcycles" {
				values.Set("vin", "SMTD40HL5NT123457")
			}
			values.Set(tt.field, tt.value)
			before := store.snapshot()
			diskBefore, _ := os.ReadFile(store.path)
			err := store.update(func(next *state) error {
				_, err := applyValues(next, tt.kind, 0, values)
				return err
			})
			if err == nil {
				t.Fatalf("accepted invalid %s", tt.name)
			}
			diskAfter, _ := os.ReadFile(store.path)
			if !reflect.DeepEqual(before, store.snapshot()) || string(diskBefore) != string(diskAfter) {
				t.Fatal("failed validation modified memory or disk")
			}
		})
	}
}

func TestActiveContractUniquenessAndCancellation(t *testing.T) {
	store := testStore(t)
	customer, motorcycle := seed(t, store)
	first := save(t, store, "contracts", 0, contractForm(customer, motorcycle))
	for _, status := range []string{"draft", "signed"} {
		values := contractForm(customer, motorcycle)
		values.Set("number", "TP/2026/002")
		values.Set("status", status)
		err := store.update(func(s *state) error {
			_, err := applyValues(s, "contracts", 0, values)
			return err
		})
		if err == nil {
			t.Fatalf("duplicate active %s contract accepted", status)
		}
	}
	values := contractForm(customer, motorcycle)
	values.Set("status", "signed")
	save(t, store, "contracts", first, values)
	if got := availability(store.snapshot(), motorcycle); got != "signed" {
		t.Fatalf("signed availability %q", got)
	}
	values.Set("status", "cancelled")
	save(t, store, "contracts", first, values)
	if got := availability(store.snapshot(), motorcycle); got != "available" {
		t.Fatal("cancellation did not release motorcycle")
	}
	values.Set("number", "TP/2026/002")
	values.Set("status", "draft")
	second := save(t, store, "contracts", 0, values)
	if len(store.snapshot().Contracts) != 2 || second == first {
		t.Fatal("replacement not saved")
	}
	// Editing an old cancelled contract back to active must also be rejected.
	values.Set("number", "TP/2026/001")
	if err := store.update(func(s *state) error {
		_, err := applyValues(s, "contracts", first, values)
		return err
	}); err == nil {
		t.Fatal("reactivating an old contract bypassed uniqueness")
	}
}

func TestUniquenessAndDeletionProtection(t *testing.T) {
	store := testStore(t)
	customer, motorcycle := seed(t, store)
	contract := save(t, store, "contracts", 0, contractForm(customer, motorcycle))
	for _, status := range []string{"draft", "signed", "cancelled"} {
		values := contractForm(customer, motorcycle)
		values.Set("status", status)
		save(t, store, "contracts", contract, values)
		for _, kind := range []string{"customer", "motorcycle"} {
			err := store.update(func(s *state) error {
				if kind == "customer" {
					delete(s.Customers, customer)
				} else {
					delete(s.Motorcycles, motorcycle)
				}
				return nil
			})
			if err == nil {
				t.Fatalf("deleted referenced %s with %s contract", kind, status)
			}
		}
	}
	if err := store.update(func(s *state) error {
		_, err := applyValues(s, "motorcycles", 0, motorcycleForm())
		return err
	}); err == nil {
		t.Fatal("duplicate inventory VIN accepted")
	}
	otherBike := motorcycleForm()
	otherBike.Set("vin", "SMTD40HL5NT123457")
	other := save(t, store, "motorcycles", 0, otherBike)
	values := contractForm(customer, other)
	values.Set("number", "tp/2026/001")
	if err := store.update(func(s *state) error {
		_, err := applyValues(s, "contracts", 0, values)
		return err
	}); err == nil {
		t.Fatal("case-insensitive duplicate contract number accepted")
	}
	if err := store.update(func(s *state) error { delete(s.Contracts, contract); return nil }); err != nil {
		t.Fatal(err)
	}
	save(t, store, "owned", 0, ownedForm(customer))
	if err := store.update(func(s *state) error { delete(s.Customers, customer); return nil }); err == nil {
		t.Fatal("deleted customer with owned motorcycle")
	}
	if err := store.update(func(s *state) error { delete(s.Motorcycles, motorcycle); return nil }); err != nil {
		t.Fatal("unreferenced motorcycle not deletable")
	}
}

func TestPersistenceFailureDoesNotPublish(t *testing.T) {
	store := testStore(t)
	before := store.snapshot()
	diskBefore, _ := os.ReadFile(store.path)
	path := store.path
	blocked := filepath.Join(testDirectory(t), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blocked, "crm.json")
	if err := store.update(func(s *state) error {
		_, err := applyValues(s, "customers", 0, customerForm())
		return err
	}); err == nil {
		t.Fatal("unwritable transaction succeeded")
	}
	diskAfter, _ := os.ReadFile(path)
	if !reflect.DeepEqual(before, store.snapshot()) || string(diskBefore) != string(diskAfter) {
		t.Fatal("failed persistence changed memory or original disk")
	}
}

func TestConcurrentWrites(t *testing.T) {
	store := testStore(t)
	const count = 20
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for i := range count {
		wg.Go(func() {
			values := customerForm()
			values.Set("first_name", fmt.Sprintf("Klient %d", i))
			errors <- store.update(func(s *state) error {
				_, err := applyValues(s, "customers", 0, values)
				return err
			})
			_ = store.snapshot()
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := openStore(store.path)
	if err != nil || len(reopened.snapshot().Customers) != count || reopened.snapshot().NextID != count+1 {
		t.Fatalf("lost concurrent updates: %v", err)
	}
}
