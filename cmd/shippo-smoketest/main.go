// Shippo smoke test — exercises the production ShippoProvider against a
// hardcoded test shipment so you can confirm the API key, request shape, and
// rate selection work without spinning up the server or hitting the DB.
//
// Flags:
//
//	--from-zip   override ship-from zip (default Rockabilly Kennewick WA)
//	--to-zip     override ship-to zip (default San Francisco CA)
//	--weight-oz  parcel weight in ounces (default 12)
//	--service    Shippo servicelevel.token (default usps_ground_advantage)
//	--format     label_file_type to request: PDF (8.5x11, default), PDF_4x6
//	             (4x6 thermal label), or PNG. Only applies with --buy.
//	--buy        actually purchase the label (default false — quote only)
//
// Without --buy the script only creates the shipment and prints the returned
// rates. With --buy it picks the requested service token and buys the label in
// the requested format, then prints the label URL — download it to eyeball how
// the format prints. Test API keys produce a watermarked label and don't move
// money, so you can buy as many as you need to dial in print settings.
//
// Usage:
//
//	export SHIPPO_API_KEY=shippo_test_xxxxxxxxxx
//	go run ./cmd/shippo-smoketest                              # quote only
//	go run ./cmd/shippo-smoketest --buy                        # quote + purchase (PDF)
//	go run ./cmd/shippo-smoketest --buy --format=PDF_4x6       # 4x6 thermal label
//	go run ./cmd/shippo-smoketest --weight-oz=80 --buy         # heavier parcel
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/dukerupert/hiri/internal/platform/shipping"
)

func main() {
	_ = godotenv.Load()

	fromZip := flag.String("from-zip", "99336", "ship-from zip")
	toZip := flag.String("to-zip", "94103", "ship-to zip")
	weightOz := flag.Float64("weight-oz", 12, "parcel weight in ounces")
	service := flag.String("service", shipping.ShippoDefaultServiceToken, "Shippo servicelevel.token to buy")
	format := flag.String("format", "PDF", "label_file_type to buy: PDF, PDF_4x6, or PNG")
	buy := flag.Bool("buy", false, "actually purchase the label (default: quote only)")
	flag.Parse()

	if !shipping.ValidLabelFileType(*format) {
		log.Fatalf("invalid --format %q; allowed: %v", *format, shipping.AllowedLabelFileTypes)
	}

	apiKey := os.Getenv("SHIPPO_API_KEY")
	if apiKey == "" {
		log.Fatal("SHIPPO_API_KEY is empty — set it in .env before running the smoke test")
	}

	provider := shipping.NewShippoProvider(apiKey)

	req := shipping.LabelRequest{
		FromName:      "Rockabilly Roasting Co.",
		FromStreet1:   "101 W Kennewick Ave",
		FromCity:      "Kennewick",
		FromState:     "WA",
		FromZip:       *fromZip,
		FromCountry:   "US",
		FromEmail:     "info@rockabillyroasting.com",
		FromPhone:     "5095852320",
		ToName:        "Mr Hippo",
		ToStreet1:     "965 Mission St #572",
		ToCity:        "San Francisco",
		ToState:       "CA",
		ToZip:         *toZip,
		ToCountry:     "US",
		ToEmail:       "mrhippo@example.com",
		ToPhone:       "4151234567",
		WeightOz:      *weightOz,
		LengthIn:      10,
		WidthIn:       8,
		HeightIn:      4,
		ServiceCode:   *service,
		LabelFileType: *format,
		Reference:     "smoketest-" + time.Now().Format("20060102-150405"),
	}

	fmt.Printf("Shippo smoke test — %s → %s, %.1f oz\n",
		req.FromZip, req.ToZip, req.WeightOz)
	fmt.Printf("Requested service: %s\n", *service)
	fmt.Printf("Label format:      %s\n", *format)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !*buy {
		fmt.Println("\n--buy not set — would call CreateLabel with the above request.")
		fmt.Println("Rerun with --buy to actually purchase a watermarked test label.")
		return
	}

	result, err := provider.CreateLabel(ctx, req)
	if err != nil {
		log.Fatalf("CreateLabel failed: %v", err)
	}

	fmt.Println("\n✓ Label purchased:")
	fmt.Printf("  Carrier:        %s\n", result.CarrierName)
	fmt.Printf("  Service:        %s\n", result.ServiceName)
	fmt.Printf("  Rate:           $%.2f %s\n", float64(result.RateCents)/100, result.Currency)
	fmt.Printf("  Tracking:       %s\n", result.TrackingNumber)
	fmt.Printf("  Label URL:      %s\n", result.LabelURL)
	fmt.Println("\nDownload the label URL to verify the PDF rendered correctly.")
}
