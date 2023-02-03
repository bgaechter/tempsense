package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/timestreamwrite"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

var log *zap.SugaredLogger

type Token struct {
	Access_token string `json:"access_token"`
	Token_type   string `json:"token_type"`
	Expires_in   string `json:"expires_in"`
}

type Device struct {
	ActiveTime int      `json:"active_time"`
	CreateTime int      `json:"create_time"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Online     bool     `json:"online"`
	Status     []Status `json:"status"`
	Sub        bool     `json:"sub"`
	TimeZone   string   `json:"time_zone"`
	UpdateTime int      `json:"update_time"`
	DeviceType string   `json:"device_type"`
}
type Status struct {
	Code  string      `json:"code"`
	Value interface{} `json:"value"`
}

type DevicesResponse struct {
	Result []Device `json:"result"`
	T      int64    `json:"t"`
}

func getDevices() *DevicesResponse {
	api_key, api_key_present := os.LookupEnv("DANFOSS_API_KEY")
	api_secret, api_secret_present := os.LookupEnv("DANFOSS_API_SECRET")

	if !api_key_present || !api_secret_present {
		log.Error("Credentials for API missing.")
	}

	auth_header := "Basic " + base64.StdEncoding.EncodeToString([]byte(api_key+":"+api_secret))
	client := http.Client{}
	form := url.Values{}
	form.Add("grant_type", "client_credentials")
	req, err := http.NewRequest("POST", "https://api.danfoss.com/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		log.Error(err)
	}

	req.Header = http.Header{
		"Authorization": {auth_header},
		"Accept":        {"application/json"},
		"Content-Type":  {"application/x-www-form-urlencoded"},
	}

	res, err := client.Do(req)
	if err != nil {
		log.Error(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Error(err)
	}
	token := Token{}
	err = json.Unmarshal([]byte(body), &token)
	if err != nil {
		panic(err)
	}

	if token == (Token{}) {
		log.Fatalf("Could not retrieve access token.\n %v", string(body))
	}
	if token.Access_token == "" {
		log.Fatalf("Could not retrieve access token.\n %v", string(body))
	}

	// Get Devices
	req, err = http.NewRequest("GET", "https://api.danfoss.com/ally/devices", nil)
	if err != nil {
		log.Error(err)
	}

	req.Header = http.Header{
		"Authorization": {"Bearer " + token.Access_token},
		"Accept":        {"application/json"},
	}

	res, err = client.Do(req)
	if err != nil {
		log.Error(err)
	}
	defer res.Body.Close()
	body, err = io.ReadAll(res.Body)
	if err != nil {
		log.Error(err)
	}

	devicesResponse := DevicesResponse{}
	err = json.Unmarshal([]byte(body), &devicesResponse)
	if err != nil {
		log.Error(err)
	}

	return &devicesResponse
}

func writeToTimestrean(devices *DevicesResponse) {
	timestreamDBEnv, is_defined := os.LookupEnv("TIMESTREAM_DATABASE")
	if !is_defined {
		log.Fatal("TIMESTREAM_DATABASE variable not set.")
	}
	timestreamTableEnv, is_defined := os.LookupEnv("TIMESTREAM_TABLE")
	if !is_defined {
		log.Fatal("TIMESTREAM_TABLE variable not set.")
	}
	tr := &http.Transport{
		ResponseHeaderTimeout: 20 * time.Second,
		Proxy:                 http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			KeepAlive: 30 * time.Second,
			DualStack: true,
			Timeout:   30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	http2.ConfigureTransport(tr)
	sess, err := session.NewSession(&aws.Config{Region: aws.String("eu-central-1"), MaxRetries: aws.Int(10), HTTPClient: &http.Client{Transport: tr}})
	if err != nil {
		log.Fatal(err)
	}
	writeSvc := timestreamwrite.New(sess)

	now := time.Now()
	currentTimeInSeconds := now.Unix()

	records := []*timestreamwrite.Record{}

	for _, device := range devices.Result {
		log.Info(device.Name)

		for _, status := range device.Status {
			if status.Code == "va_temperature" || status.Code == "temp_current" {
				val, err := interfaceToFloat(status.Value)
				if err != nil {
					log.Error(err)
				}
				val = val / 10
				records = append(records, &timestreamwrite.Record{
					Dimensions: []*timestreamwrite.Dimension{
						{
							Name:  aws.String("name"),
							Value: aws.String(device.Name),
						},
						{
							Name:  aws.String("id"),
							Value: aws.String(device.ID),
						},
						{
							Name:  aws.String("type"),
							Value: aws.String(device.DeviceType),
						},
					},
					MeasureName:      aws.String("temperature"),
					MeasureValue:     aws.String(strconv.FormatFloat(val, 'f', 2, 64)),
					MeasureValueType: aws.String("DOUBLE"),
					Time:             aws.String(strconv.FormatInt(currentTimeInSeconds, 10)),
					TimeUnit:         aws.String("SECONDS"),
				})
			}
		}
	}

	writeRecordsInput := &timestreamwrite.WriteRecordsInput{
		DatabaseName: aws.String(timestreamDBEnv),
		TableName:    aws.String(timestreamTableEnv),
		Records:      records,
	}
	_, err = writeSvc.WriteRecords(writeRecordsInput)

	if err != nil {
		log.Fatal(err)
	} else {
		log.Info("Write records is successful")
	}
}

func interfaceToFloat(v interface{}) (float64, error) {
	switch v := v.(type) {
	case int:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(v, 32)
	case bool:
		return 0.0, nil
	default:
		return 0.0, fmt.Errorf("conversion to float from %T not supported", v)
	}
}

func handler(request events.CloudWatchEvent) (string, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync() // flushes buffer, if any
	log = logger.Sugar()

	deviceResponse := getDevices()
	writeToTimestrean(deviceResponse)
	return request.Source, nil
}

func main() {

	lambda.Start(handler)
}
