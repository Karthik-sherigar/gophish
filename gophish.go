package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/gophish/gophish/config"
	"github.com/gophish/gophish/controllers"
	"github.com/gophish/gophish/dialer"
	"github.com/gophish/gophish/imap"
	log "github.com/gophish/gophish/logger"
	"github.com/gophish/gophish/middleware"
	"github.com/gophish/gophish/models"
	"github.com/gophish/gophish/webhook"
)

const (
	modeAll   string = "all"
	modeAdmin string = "admin"
	modePhish string = "phish"
)

var (
	configPath    = kingpin.Flag("config", "Location of config.json.").Default("./config.json").String()
	disableMailer = kingpin.Flag("disable-mailer", "Disable the mailer (for use with multi-system deployments)").Bool()
	mode          = kingpin.Flag("mode", fmt.Sprintf("Run the binary in one of the modes (%s, %s or %s)", modeAll, modeAdmin, modePhish)).
				Default("all").Enum(modeAll, modeAdmin, modePhish)
)

func main() {
	// Read and set the version
	version, err := ioutil.ReadFile("./VERSION")
	if err != nil {
		log.Fatal(err)
	}
	kingpin.Version(string(version))

	// Parse command line flags
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Parse()

	// Load config
	conf, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if conf.ContactAddress == "" {
		log.Warnf("No contact address has been configured.")
		log.Warnf("Please consider adding a contact_address entry in your config.json")
	}
	config.Version = string(version)

	// Configure network restrictions for outbound traffic
	dialer.SetAllowedHosts(conf.AdminConf.AllowedInternalHosts)
	webhook.SetTransport(&http.Transport{
		DialContext: dialer.Dialer().DialContext,
	})

	// Setup logger
	err = log.Setup(conf.Logging)
	if err != nil {
		log.Fatal(err)
	}

	// Setup DB and global models
	err = models.Setup(conf)
	if err != nil {
		log.Fatal(err)
	}

	// Unlock all mail logs (in case of crash)
	err = models.UnlockAllMailLogs()
	if err != nil {
		log.Fatal(err)
	}

	// Initialize admin and phishing servers
	adminOptions := []controllers.AdminServerOption{}
	if *disableMailer {
		adminOptions = append(adminOptions, controllers.WithWorker(nil))
	}

	adminServer := controllers.NewAdminServer(conf.AdminConf, adminOptions...)
	middleware.Store.Options.Secure = conf.AdminConf.UseTLS

	phishServer := controllers.NewPhishingServer(conf.PhishConf)

	imapMonitor := imap.NewMonitor()

	// Start servers based on mode
	if *mode == modeAdmin || *mode == modeAll {
		go adminServer.Start()
		go imapMonitor.Start()
	}
	if *mode == modePhish || *mode == modeAll {
		go phishServer.Start()
	}

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	log.Info("CTRL+C Received... Gracefully shutting down servers")
	if *mode == modeAdmin || *mode == modeAll {
		adminServer.Shutdown()
		imapMonitor.Shutdown()
	}
	if *mode == modePhish || *mode == modeAll {
		phishServer.Shutdown()
	}
}
