package Control

import (
	"fmt"
	"lab3/src"
	"strings"
)

const (
	p = 1.333
)


func printSeparator(title string) {
	width := 60
	padding := (width - len(title) - 2) / 2
	fmt.Printf("\n%s %s %s\n", 
		strings.Repeat("=", padding), 
		title, 
		strings.Repeat("=", padding))
}

func printSubtitle(subtitle string) {
	fmt.Printf("\n🏷️  %s\n", subtitle)
	fmt.Println(strings.Repeat("-", 40))
}

func printDescription(description *src.Description) {
	printSeparator("LOCATION DESCRIPTION")
	
	fmt.Printf("📍 %s\n", description.Name)
	fmt.Printf("🆔 %s\n", description.Xid)
	
	printSubtitle("Address Information")
	fmt.Printf("🏙️  City: %s\n", description.AddressInfo.City)
	fmt.Printf("🏛️  County: %s\n", description.AddressInfo.County)
	fmt.Printf("🇺🇳 Country: %s\n", description.AddressInfo.Country)
	fmt.Printf("🗺️  State: %s\n", description.AddressInfo.State)
	fmt.Printf("📮 Postcode: %s\n", description.AddressInfo.Postcode)
	fmt.Printf("🏘️  Suburb: %s\n", description.AddressInfo.Suburb)
	fmt.Printf("🏡 City Districts: %s\n", description.AddressInfo.CityDistricts)
	
	fmt.Println(strings.Repeat("=", 60))
}

func printIntPlaces(places *src.PlacesInfo) {
	printSeparator("INTERESTING PLACES")
	
	if len(places.Features) == 0 {
		fmt.Println("❌ No interesting places found")
	} else {
		for i, pl := range places.Features {
			fmt.Printf("%2d. 🏛️  %s\n", i+1, pl.Prop.Name)
			fmt.Printf("    🆔 %s\n", pl.Prop.Xid)
			if i < len(places.Features)-1 {
				fmt.Println("    " + strings.Repeat("-", 40))
			}
		}
	}
	
	fmt.Println(strings.Repeat("=", 60))
}

func printWeather(weather *src.Weather) {
	printSeparator("WEATHER INFORMATION")
	
	wh_inf := weather.WeatherInf
	wh_main := weather.Main
	wh_wind := weather.Wind
	
	if len(wh_inf) > 0 {
		printSubtitle("Weather Conditions")
		for _, inf := range wh_inf {
			fmt.Printf("🌤️  %s - %s\n", inf.Main, inf.Description)
		}
	}
	
	printSubtitle("Temperature & Humidity")
	fmt.Printf("🌡️  Temperature: %.1f°C\n", wh_main.Temp-273.15)
	fmt.Printf("💧 Humidity: %d%%\n", wh_main.Humidity)
	fmt.Printf("🤔 Feels like: %.1f°C\n", wh_main.FeelsLike-273.15)
	fmt.Printf("📊 Pressure: %f millimeters of mercury\n",float64(wh_main.Pressure) / p)
	
	printSubtitle("Wind")
	fmt.Printf("💨 Wind Speed: %.1f m/s\n", wh_wind.Speed)
	if wh_wind.Deg != 0 {
		fmt.Printf("🧭 Wind Direction: %d°\n", wh_wind.Deg)
	}
	
	fmt.Println(strings.Repeat("=", 60))
}

func printLocations(places []src.Place) {
	printSeparator("AVAILABLE LOCATIONS")
	
	if len(places) == 0 {
		fmt.Println("❌ No locations found")
	} else {
		for i, place := range places {
			fmt.Printf("%2d. 📍 %s\n", i+1, place.Name)
			fmt.Printf("    📍 Coordinates: (%.4f, %.4f)\n", 
				place.Point.Lat, place.Point.Lng)
			fmt.Printf("    🏳️  Country: %s\n", place.Country)
			if place.City != "" {
				fmt.Printf("    🏙️  City: %s\n", place.City)
			}
			fmt.Printf("    Osm key : %s\n", place.Osm_key)
			fmt.Printf("    Osm value : %s\n", place.Osm_value)

			if i < len(places)-1 {
				fmt.Println("    " + strings.Repeat("─", 50))
			}
		}
	}
	
	fmt.Println(strings.Repeat("=", 60))
}

