package main

import (
	"crypto/sha256"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type config struct {
	Address, DataFile, Username, Password string
	SecureCookie                          bool
}

func loadConfig() (config, error) {
	c := config{
		Address: os.Getenv("CRM_ADDR"), DataFile: os.Getenv("CRM_DATA_FILE"),
		Username: os.Getenv("CRM_USERNAME"), Password: os.Getenv("CRM_PASSWORD"),
	}
	if c.Username == "" || c.Password == "" {
		return c, errors.New("CRM_USERNAME and CRM_PASSWORD are required; no default credentials are provided")
	}
	if c.Address == "" {
		c.Address = "127.0.0.1:8080"
	}
	if c.DataFile == "" {
		c.DataFile = "data/crm.json"
	}
	if value := os.Getenv("CRM_SECURE_COOKIE"); value != "" {
		var err error
		c.SecureCookie, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("CRM_SECURE_COOKIE must be true or false")
		}
	}
	return c, nil
}

func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	store, err := openStore(c.DataFile)
	if err != nil {
		log.Fatal(err)
	}
	app := newApp(store, sha256.Sum256([]byte(c.Username)), sha256.Sum256([]byte(c.Password)), c.SecureCookie)
	server := &http.Server{
		Addr: c.Address, Handler: app,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	log.Printf("Triumph Polska CRM listening on %s (single-process JSON storage)", c.Address)
	log.Fatal(server.ListenAndServe())
}
