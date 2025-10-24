package src

type Place struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	City    string `json:"city"`
	State   string `json:"state"`
	Point   Point  `json:"point"`
	Osm_key string `json:"osm_key"`
	Osm_value string `json:"osm_value"`
}

type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type GeocodeResponse struct {
	Hits   []Place `json:"hits"`
	Locale string  `json:"locale"`
}

type Weather struct {
	WeatherInf []WeatherInfo `json:"weather"`
	Main       MainTemp      `json:"main"`
	Wind       WindData      `json:"wind"`
}

type WeatherInfo struct {
	Id          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
}

type MainTemp struct {
	Temp      float64 `json:"temp"`
	FeelsLike float64 `json:"feels_like"`
	Humidity  int     `json:"humidity"`
	Pressure  int     `json:"pressure"`
}

type WindData struct {
	Speed float64 `json:"speed"`
	Deg   int     `json:"deg"`
}

type PlacesInfo struct {
	Features []PlaceInfo `json:"features"`
	Type     string      `json:"type"`
}

type PlaceInfo struct {
	Type string     `json:"type"`
	Prop Properties `json:"properties"`
}

type Properties struct {
	Xid   string `json:"xid"`
	Name  string `json:"name"`
	Kinds string `json:"kinds"`
}

type Description struct {
	Xid         string  `json:"xid"`
	Name        string  `json:"name"`
	AddressInfo Address `json:"address"`
}

type Address struct {
	City          string `json:"city"`
	State         string `json:"state"`
	County        string `json:"county"`
	Suburb        string `json:"suburb"`
	Country       string `json:"country"`
	Postcode      string `json:"postcode"`
	CityDistricts string `json:"city_district"`
}
