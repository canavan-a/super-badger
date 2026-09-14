package main

import (
	"context"
	"log"

	"github.com/joho/godotenv"

	"main/api"
	"main/config"
	"main/database"
	"main/metrics"
	"main/mullvad"
	"main/notify"
	"main/opencode"
	"main/station"
)

func main() {
	// .env is optional: load it for local dev, fall back to the real
	// environment (systemd / nix) when it isn't present.
	_ = godotenv.Load()

	cfg := config.Load()

	db, err := database.Connect(cfg.DBPath)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}

	oc := opencode.NewClient(cfg.OpencodeBaseURL, cfg.OpencodePassword)
	broker := opencode.NewEventBroker(oc)
	svc := station.NewService(db, oc, broker)
	mv := mullvad.NewClient(cfg.MullvadBin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go broker.Run(ctx)

	hub := notify.NewHub()
	notifier := metrics.NotifierFunc(func(st database.Station, key string, value float64, direction database.ThresholdDirection) {
		hub.Broadcast(notify.Msg{
			"type":         "datapoint_threshold",
			"station_id":   st.ID,
			"station_name": st.Name,
			"key":          key,
			"value":        value,
			"direction":    direction,
		})
	})
	mm := metrics.NewManager(db, notifier)
	mm.Start(ctx)

	router := api.NewRouter(svc, oc, mv, broker, db, mm, hub)
	log.Printf("superbadger listening on %s, opencode at %s", cfg.ListenAddr, cfg.OpencodeBaseURL)
	if err := router.Run(cfg.ListenAddr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
