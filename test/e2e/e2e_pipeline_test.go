package e2e

import (
	"bytes"
	"context"
	"crypto/sha512"
	"hash"
	"io"
	"net/http"
	"testing"
	"time"

	"eco-platform-api-gateway/pkg/generated/weather"
	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"
	"google.golang.org/protobuf/proto"
)

func Test_EndToEnd_Telemetry_Pipeline(t *testing.T) {
	uniqueStationID := "15"

	weatherPacket := &weather.WeatherPacket{
		StationId: uniqueStationID,
		Timestamp: time.Now().UnixMilli(),
		Location:  &weather.Location{},
		Readings: []*weather.SensorReading{
			{
				SensorData: &weather.SensorReading_Ambient{
					Ambient: &weather.AmbientReading{
						TemperatureC:   24.5,
						HumidityPct:    62.0,
						PressureHpa:    1013.25,
						LeafWetnessPct: 0.0,
					},
				},
			},
		},
	}

	protobufBinaryBytes, err := proto.Marshal(weatherPacket)
	if err != nil {
		t.Fatalf("CRITICAL: Failed to serialize Proto packet payload: %v", err)
	}

	consumerConfig := sarama.NewConfig()
	consumerConfig.Net.SASL.Enable = true
	consumerConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
	consumerConfig.Net.SASL.User = "client"
	consumerConfig.Net.SASL.Password = "client-secret-pass"
	consumerConfig.Net.SASL.Version = sarama.SASLHandshakeV1
	consumerConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
		return &XDGSCRAMClient{Hash: func() hash.Hash { return sha512.New() }}
	}
	consumerConfig.Consumer.Offsets.Initial = sarama.OffsetOldest

	client, err := sarama.NewClient([]string{"localhost:9094"}, consumerConfig)
	if err != nil {
		t.Fatalf("CRITICAL: Failed to link base Kafka client: %v", err)
	}
	defer func(client sarama.Client) {
		err := client.Close()
		if err != nil {
			t.Logf("WARN: Failed to close Kafka client: %v", err)
		}
	}(client)

	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		t.Fatalf("CRITICAL: Failed to spin up Go Kafka Consumer: %v", err)
	}
	defer func(consumer sarama.Consumer) {
		err := consumer.Close()
		if err != nil {
			t.Logf("WARN: Failed to close Kafka consumer: %v", err)
		}
	}(consumer)

	topic := "environment.weather.telemetry.live"
	const partitionCount = 6

	var partitionConsumers []sarama.PartitionConsumer

	for p := int32(0); p < partitionCount; p++ {
		startingOffset, err := client.GetOffset(topic, p, sarama.OffsetNewest)
		if err != nil {
			t.Fatalf("CRITICAL: Failed to fetch partition %d write head boundary: %v", p, err)
		}

		pConsumer, err := consumer.ConsumePartition(topic, p, startingOffset)
		if err != nil {
			t.Fatalf("CRITICAL: Failed to consume partition %d: %v", p, err)
		}
		partitionConsumers = append(partitionConsumers, pConsumer)
	}

	defer func() {
		for _, pc := range partitionConsumers {
			_ = pc.Close()
		}
	}()

	// Dispatch HTTP Request (STIMULUS)
	gatewayTargetURL := "http://localhost:8000/api/v1/telemetry/mono"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", gatewayTargetURL, bytes.NewReader(protobufBinaryBytes))
	if err != nil {
		t.Fatalf("CRITICAL: Failed to allocate HTTP request frame: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("FAIL: Edge gateway connection rejected or timed out: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			t.Logf("WARN: Failed to close response body: %v", err)
		}
	}(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("FAIL: Expected HTTP status 202 Accepted, but got: %d", resp.StatusCode)
	}

	var receivedPackets []*weather.WeatherPacket
	foundMatch := false

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

pollLoop:
	for {
		select {
		case <-timeoutCtx.Done():
			break pollLoop

		default:
			messageFoundThisCycle := false
			for _, pc := range partitionConsumers {
				select {
				case msg := <-pc.Messages():
					packet := &weather.WeatherPacket{}
					if err := proto.Unmarshal(msg.Value, packet); err != nil {
						continue
					}
					receivedPackets = append(receivedPackets, packet)
					messageFoundThisCycle = true
				default:
					continue
				}
			}

			for _, p := range receivedPackets {
				if p.GetStationId() == uniqueStationID {
					foundMatch = true
					break pollLoop
				}
			}

			if !messageFoundThisCycle {
				select {
				case <-timeoutCtx.Done():
					break pollLoop
				case <-time.After(300 * time.Millisecond):
				}
			}
		}
	}

	if !foundMatch {
		t.Fatalf("FAIL: Expected WeatherPacket with ID %s was not received by any of the %d Kafka partitions", uniqueStationID, partitionCount)
	}
}

type XDGSCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	Hash scram.HashGeneratorFcn
}

func (x *XDGSCRAMClient) Begin(userName, password, authzID string) (err error) {
	x.Client, err = x.Hash.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.ClientConversation = x.Client.NewConversation()
	return nil
}

func (x *XDGSCRAMClient) Step(challenge string) (response string, err error) {
	return x.ClientConversation.Step(challenge)
}

func (x *XDGSCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}
