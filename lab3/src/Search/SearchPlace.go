package Search

import (
	"encoding/json"
	"fmt"
	"io"
	"lab3/src"
	"net/http"
	"net/url"
	"strconv"
	"lab3/cmd/config"
)

const (

	limit = 10 // {"message":"Free packages cannot use a 'limit' parameter higher than 10"}
	
)

func getFullUrl(namePlace string) string {
	params := url.Values{}
	params.Add("q", namePlace)
	params.Add("key", config.GraphHopperAPIKey)
	params.Add("limit", strconv.Itoa(limit))
	return config.GraphHopperAPI + "?" + params.Encode()
}

func SearchPlace(namePlace string) ([]src.Place, error) {
	fullUrl := getFullUrl(namePlace)

	resp, err := http.Get(fullUrl)
	if err != nil {
		fmt.Printf("Error fetching data: %v\n", err)
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)


	fmt.Println(string(body))
	var response src.GeocodeResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("Failed parse json")
		return nil, err
	}

	fmt.Printf("Found %d locations:\n", len(response.Hits))

	return response.Hits, nil
}
