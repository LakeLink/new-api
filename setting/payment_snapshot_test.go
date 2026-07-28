package setting

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestPaymentSettingsSnapshotIsCoherentDuringRuntimeUpdates(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	original := common.OptionMap
	common.OptionMap = map[string]string{
		"StripeApiSecret":     "sk_generation_1",
		"StripeWebhookSecret": "whsec_generation_1",
		"StripeUnitPrice":     "1",
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = original
		common.OptionMapRWMutex.Unlock()
	})

	start := make(chan struct{})
	errCh := make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2_000; i++ {
			generation := "1"
			if i%2 == 0 {
				generation = "2"
			}
			common.OptionMapRWMutex.Lock()
			common.OptionMap["StripeApiSecret"] = "sk_generation_" + generation
			common.OptionMap["StripeWebhookSecret"] = "whsec_generation_" + generation
			common.OptionMap["StripeUnitPrice"] = generation
			common.OptionMapRWMutex.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 4_000 {
			snapshot := GetStripeSettings()
			if snapshot.APISecret == "sk_generation_1" {
				if snapshot.WebhookSecret != "whsec_generation_1" || snapshot.UnitPrice != 1 {
					select {
					case errCh <- "generation 1 snapshot was torn":
					default:
					}
					return
				}
			} else if snapshot.APISecret == "sk_generation_2" {
				if snapshot.WebhookSecret != "whsec_generation_2" || snapshot.UnitPrice != 2 {
					select {
					case errCh <- "generation 2 snapshot was torn":
					default:
					}
					return
				}
			} else {
				select {
				case errCh <- "unexpected API key":
				default:
				}
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errCh)

	var failure string
	for failure = range errCh {
	}
	assert.Empty(t, failure)
}
