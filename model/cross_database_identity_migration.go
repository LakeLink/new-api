package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const identityMigrationBatchSize = 500

func prepareCrossDatabaseIdentityColumns() error {
	if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return nil
	}
	columns := []struct {
		table  string
		model  any
		column string
	}{
		{"models", &Model{}, "name_hash"},
		{"vendors", &Vendor{}, "name_hash"},
		{"prefill_groups", &PrefillGroup{}, "name_hash"},
		{"passkey_credentials", &PasskeyCredential{}, "credential_id_hash"},
		{"top_ups", &TopUp{}, "trade_no_hash"},
		{"top_ups", &TopUp{}, "provider_payment_hash"},
		{"subscription_orders", &SubscriptionOrder{}, "trade_no_hash"},
		{"subscription_orders", &SubscriptionOrder{}, "provider_subscription_hash"},
		{"user_subscriptions", &UserSubscription{}, "provider_subscription_hash"},
		{"subscription_provider_payments", &SubscriptionProviderPayment{}, "payment_identity_hash"},
		{"casbin_rule", &CasbinRule{}, "rule_hash"},
		{"perf_metrics", &PerfMetric{}, "identity_hash"},
	}
	for _, column := range columns {
		if !DB.Migrator().HasTable(column.table) ||
			DB.Migrator().HasColumn(column.model, column.column) {
			continue
		}
		if err := DB.Exec(
			"ALTER TABLE `" + column.table + "` ADD COLUMN `" + column.column + "` char(64)",
		).Error; err != nil {
			return fmt.Errorf("add SQLite identity column %s.%s: %w", column.table, column.column, err)
		}
	}
	return nil
}

// migrateCrossDatabaseIdentityHashes replaces uniqueness boundaries that rely
// on NULL semantics, partial indexes, database collations, or oversized
// compound string indexes with compact deterministic hashes.
func migrateCrossDatabaseIdentityHashes() error {
	if err := migrateNamedResourceIdentityHashes(); err != nil {
		return err
	}
	if err := migratePasskeyCredentialHashes(); err != nil {
		return err
	}
	if err := migrateTopUpIdentityHashes(); err != nil {
		return err
	}
	if err := migrateSubscriptionIdentityHashes(); err != nil {
		return err
	}
	if err := migrateCasbinRuleHashes(); err != nil {
		return err
	}
	if err := migratePerfMetricHashes(); err != nil {
		return err
	}

	legacyIndexes := []struct {
		model any
		name  string
	}{
		{&Model{}, "uk_model_name_delete_at"},
		{&Vendor{}, "uk_vendor_name_delete_at"},
		{&PrefillGroup{}, "uk_prefill_name"},
		{&PasskeyCredential{}, "idx_passkey_credentials_credential_id"},
		{&TopUp{}, "idx_top_ups_trade_no"},
		{&SubscriptionOrder{}, "idx_subscription_orders_trade_no"},
		{&SubscriptionOrder{}, "idx_subscription_orders_provider_customer_id"},
		{&SubscriptionOrder{}, "idx_subscription_orders_provider_subscription_id"},
		{&SubscriptionOrder{}, "idx_subscription_orders_provider_payment_id"},
		{&UserSubscription{}, "idx_user_subscriptions_provider_subscription_id"},
		{&SubscriptionProviderPayment{}, "idx_subscription_provider_payment"},
		{&CasbinRule{}, "idx_casbin_rule"},
		{&CasbinRule{}, "idx_casbin_rule_unique"},
		{&PerfMetric{}, "idx_perf_model_group_bucket"},
	}
	migrator := DB.Migrator()
	for _, legacyIndex := range legacyIndexes {
		if !migrator.HasIndex(legacyIndex.model, legacyIndex.name) {
			continue
		}
		if err := migrator.DropIndex(legacyIndex.model, legacyIndex.name); err != nil {
			return fmt.Errorf("drop legacy index %s: %w", legacyIndex.name, err)
		}
	}
	return nil
}

func migrateNamedResourceIdentityHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var models []Model
		if err := lockForUpdate(tx.Unscoped()).
			FindInBatches(&models, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range models {
					model := &models[i]
					if model.DeletedAt.Valid {
						if model.NameHash == nil {
							continue
						}
						if err := tx.Unscoped().Model(&Model{}).
							Where("id = ?", model.Id).
							UpdateColumn("name_hash", nil).Error; err != nil {
							return fmt.Errorf("clear deleted model %d identity: %w", model.Id, err)
						}
						continue
					}
					name, nameHash, err := normalizeNamedResourceIdentity(model.ModelName, 128)
					if err != nil {
						return fmt.Errorf("migrate model %d identity: %w", model.Id, err)
					}
					if model.ModelName == name && model.NameHash != nil && *model.NameHash == nameHash {
						continue
					}
					if err := tx.Model(&Model{}).Where("id = ?", model.Id).
						UpdateColumns(map[string]any{
							"model_name": name,
							"name_hash":  nameHash,
						}).Error; err != nil {
						return fmt.Errorf("migrate model %d identity: %w", model.Id, err)
					}
				}
				return nil
			}).Error; err != nil {
			return err
		}

		var vendors []Vendor
		if err := lockForUpdate(tx.Unscoped()).
			FindInBatches(&vendors, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range vendors {
					vendor := &vendors[i]
					if vendor.DeletedAt.Valid {
						if vendor.NameHash == nil {
							continue
						}
						if err := tx.Unscoped().Model(&Vendor{}).
							Where("id = ?", vendor.Id).
							UpdateColumn("name_hash", nil).Error; err != nil {
							return fmt.Errorf("clear deleted vendor %d identity: %w", vendor.Id, err)
						}
						continue
					}
					name, nameHash, err := normalizeNamedResourceIdentity(vendor.Name, 128)
					if err != nil {
						return fmt.Errorf("migrate vendor %d identity: %w", vendor.Id, err)
					}
					if vendor.Name == name && vendor.NameHash != nil && *vendor.NameHash == nameHash {
						continue
					}
					if err := tx.Model(&Vendor{}).Where("id = ?", vendor.Id).
						UpdateColumns(map[string]any{
							"name":      name,
							"name_hash": nameHash,
						}).Error; err != nil {
						return fmt.Errorf("migrate vendor %d identity: %w", vendor.Id, err)
					}
				}
				return nil
			}).Error; err != nil {
			return err
		}

		var groups []PrefillGroup
		return lockForUpdate(tx.Unscoped()).
			FindInBatches(&groups, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range groups {
					group := &groups[i]
					if group.DeletedAt.Valid {
						if group.NameHash == nil {
							continue
						}
						if err := tx.Unscoped().Model(&PrefillGroup{}).
							Where("id = ?", group.Id).
							UpdateColumn("name_hash", nil).Error; err != nil {
							return fmt.Errorf("clear deleted prefill group %d identity: %w", group.Id, err)
						}
						continue
					}
					name, nameHash, err := normalizeNamedResourceIdentity(group.Name, 64)
					if err != nil {
						return fmt.Errorf("migrate prefill group %d identity: %w", group.Id, err)
					}
					if group.Name == name && group.NameHash != nil && *group.NameHash == nameHash {
						continue
					}
					if err := tx.Model(&PrefillGroup{}).Where("id = ?", group.Id).
						UpdateColumns(map[string]any{
							"name":      name,
							"name_hash": nameHash,
						}).Error; err != nil {
						return fmt.Errorf("migrate prefill group %d identity: %w", group.Id, err)
					}
				}
				return nil
			}).Error
	})
}

func migratePasskeyCredentialHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var credentials []PasskeyCredential
		return lockForUpdate(tx.Unscoped()).
			FindInBatches(&credentials, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range credentials {
					credential := &credentials[i]
					if credential.DeletedAt.Valid {
						if credential.CredentialIDHash == nil {
							continue
						}
						if err := tx.Unscoped().Model(&PasskeyCredential{}).
							Where("id = ?", credential.ID).
							UpdateColumn("credential_id_hash", nil).Error; err != nil {
							return fmt.Errorf("clear deleted passkey %d identity: %w", credential.ID, err)
						}
						continue
					}
					if credential.UserID <= 0 {
						return fmt.Errorf("passkey %d has invalid user ID", credential.ID)
					}
					credentialID, credentialIDHash, err := normalizePasskeyCredentialID(credential.CredentialID)
					if err != nil {
						return fmt.Errorf("migrate passkey %d identity: %w", credential.ID, err)
					}
					if credential.CredentialID == credentialID &&
						credential.CredentialIDHash != nil &&
						*credential.CredentialIDHash == credentialIDHash {
						continue
					}
					if err := tx.Model(&PasskeyCredential{}).Where("id = ?", credential.ID).
						UpdateColumns(map[string]any{
							"credential_id":      credentialID,
							"credential_id_hash": credentialIDHash,
						}).Error; err != nil {
						return fmt.Errorf("migrate passkey %d identity: %w", credential.ID, err)
					}
				}
				return nil
			}).Error
	})
}

func migrateTopUpIdentityHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var topUps []TopUp
		return lockForUpdate(tx).
			FindInBatches(&topUps, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range topUps {
					topUp := &topUps[i]
					tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(topUp.TradeNo)
					if err != nil {
						return fmt.Errorf("migrate topup %d trade identity: %w", topUp.Id, err)
					}
					updates := map[string]any{
						"trade_no":      tradeNo,
						"trade_no_hash": tradeNoHash,
					}
					if topUp.ProviderPaymentId == "" {
						if topUp.TradeNo == tradeNo &&
							topUp.TradeNoHash != nil &&
							*topUp.TradeNoHash == tradeNoHash &&
							topUp.ProviderPaymentHash == nil {
							continue
						}
						updates["provider_payment_hash"] = nil
					} else {
						paymentProvider := inferLegacyPaymentProvider(
							topUp.PaymentProvider,
							topUp.PaymentMethod,
						)
						paymentProvider, paymentID, paymentHash, err :=
							normalizeProviderPaymentIdentity(paymentProvider, topUp.ProviderPaymentId)
						if err != nil {
							return fmt.Errorf("migrate topup %d provider payment identity: %w", topUp.Id, err)
						}
						if topUp.TradeNo == tradeNo &&
							topUp.TradeNoHash != nil &&
							*topUp.TradeNoHash == tradeNoHash &&
							topUp.PaymentProvider == paymentProvider &&
							topUp.ProviderPaymentId == paymentID &&
							topUp.ProviderPaymentHash != nil &&
							*topUp.ProviderPaymentHash == paymentHash {
							continue
						}
						updates["payment_provider"] = paymentProvider
						updates["provider_payment_id"] = paymentID
						updates["provider_payment_hash"] = paymentHash
					}
					if err := tx.Model(&TopUp{}).Where("id = ?", topUp.Id).
						UpdateColumns(updates).Error; err != nil {
						return fmt.Errorf("migrate topup %d identities: %w", topUp.Id, err)
					}
				}
				return nil
			}).Error
	})
}

func inferLegacyPaymentProvider(paymentProvider string, paymentMethod string) string {
	if paymentProvider != "" {
		return paymentProvider
	}
	switch paymentMethod {
	case PaymentMethodStripe:
		return PaymentProviderStripe
	case PaymentMethodCreem:
		return PaymentProviderCreem
	case PaymentMethodWaffo:
		return PaymentProviderWaffo
	case PaymentMethodWaffoPancake:
		return PaymentProviderWaffoPancake
	default:
		return PaymentProviderEpay
	}
}

func migrateSubscriptionIdentityHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var subscriptionOrders []SubscriptionOrder
		if err := lockForUpdate(tx).
			FindInBatches(&subscriptionOrders, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range subscriptionOrders {
					order := &subscriptionOrders[i]
					tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(order.TradeNo)
					if err != nil {
						return fmt.Errorf(
							"migrate subscription order %d trade identity: %w",
							order.Id,
							err,
						)
					}
					updates := map[string]any{
						"trade_no":                   tradeNo,
						"trade_no_hash":              tradeNoHash,
						"provider_subscription_hash": nil,
					}
					paymentProvider := order.PaymentProvider
					if paymentProvider == "" &&
						(order.ProviderSubscriptionId != "" || order.ProviderPaymentId != "") {
						paymentProvider = inferLegacyPaymentProvider(
							order.PaymentProvider,
							order.PaymentMethod,
						)
					}
					if paymentProvider != "" {
						paymentProvider, err = normalizePaymentProvider(paymentProvider)
						if err != nil {
							return fmt.Errorf(
								"migrate subscription order %d payment provider: %w",
								order.Id,
								err,
							)
						}
						updates["payment_provider"] = paymentProvider
					}
					if order.ProviderSubscriptionId != "" {
						normalizedProvider, providerSubscriptionID, providerSubscriptionHash, err :=
							normalizeProviderSubscriptionIdentity(
								paymentProvider,
								order.ProviderSubscriptionId,
							)
						if err != nil {
							return fmt.Errorf(
								"migrate subscription order %d provider subscription identity: %w",
								order.Id,
								err,
							)
						}
						updates["payment_provider"] = normalizedProvider
						updates["provider_subscription_id"] = providerSubscriptionID
						updates["provider_subscription_hash"] = providerSubscriptionHash
						paymentProvider = normalizedProvider
					}
					if order.ProviderPaymentId != "" {
						normalizedProvider, providerPaymentID, _, err :=
							normalizeProviderPaymentIdentity(
								paymentProvider,
								order.ProviderPaymentId,
							)
						if err != nil {
							return fmt.Errorf(
								"migrate subscription order %d provider payment identity: %w",
								order.Id,
								err,
							)
						}
						updates["payment_provider"] = normalizedProvider
						updates["provider_payment_id"] = providerPaymentID
					}
					if err := tx.Model(&SubscriptionOrder{}).
						Where("id = ?", order.Id).
						UpdateColumns(updates).Error; err != nil {
						return fmt.Errorf(
							"migrate subscription order %d identities: %w",
							order.Id,
							err,
						)
					}
				}
				return nil
			}).Error; err != nil {
			return err
		}

		var subscriptions []UserSubscription
		if err := lockForUpdate(tx).
			FindInBatches(&subscriptions, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range subscriptions {
					subscription := &subscriptions[i]
					updates := map[string]any{
						"provider_subscription_hash": nil,
					}
					paymentProvider := subscription.PaymentProvider
					if subscription.ProviderSubscriptionId != "" && paymentProvider == "" {
						if subscription.SubscriptionOrderId <= 0 {
							return fmt.Errorf(
								"migrate user subscription %d: payment provider is missing",
								subscription.Id,
							)
						}
						var order SubscriptionOrder
						if err := tx.Select("payment_provider", "payment_method").
							Where("id = ?", subscription.SubscriptionOrderId).
							First(&order).Error; err != nil {
							return fmt.Errorf(
								"resolve user subscription %d payment provider: %w",
								subscription.Id,
								err,
							)
						}
						paymentProvider = inferLegacyPaymentProvider(
							order.PaymentProvider,
							order.PaymentMethod,
						)
					}
					if subscription.ProviderSubscriptionId != "" {
						normalizedProvider, providerSubscriptionID, providerSubscriptionHash, err :=
							normalizeProviderSubscriptionIdentity(
								paymentProvider,
								subscription.ProviderSubscriptionId,
							)
						if err != nil {
							return fmt.Errorf(
								"migrate user subscription %d provider identity: %w",
								subscription.Id,
								err,
							)
						}
						updates["payment_provider"] = normalizedProvider
						updates["provider_subscription_id"] = providerSubscriptionID
						updates["provider_subscription_hash"] = providerSubscriptionHash
					} else if paymentProvider != "" {
						normalizedProvider, err := normalizePaymentProvider(paymentProvider)
						if err != nil {
							return fmt.Errorf(
								"migrate user subscription %d payment provider: %w",
								subscription.Id,
								err,
							)
						}
						updates["payment_provider"] = normalizedProvider
					}
					if err := tx.Model(&UserSubscription{}).
						Where("id = ?", subscription.Id).
						UpdateColumns(updates).Error; err != nil {
						return fmt.Errorf(
							"migrate user subscription %d provider identity: %w",
							subscription.Id,
							err,
						)
					}
				}
				return nil
			}).Error; err != nil {
			return err
		}

		var references []SubscriptionProviderPayment
		if err := lockForUpdate(tx).
			FindInBatches(&references, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range references {
					reference := &references[i]
					paymentProvider := reference.PaymentProvider
					if paymentProvider == "" {
						var order SubscriptionOrder
						if err := tx.Select("payment_provider", "payment_method").
							Where("id = ?", reference.SubscriptionOrderId).
							First(&order).Error; err != nil {
							return fmt.Errorf(
								"resolve subscription payment reference %d provider: %w",
								reference.Id,
								err,
							)
						}
						paymentProvider = inferLegacyPaymentProvider(
							order.PaymentProvider,
							order.PaymentMethod,
						)
					}
					paymentProvider, paymentID, paymentHash, err :=
						normalizeProviderPaymentIdentity(paymentProvider, reference.ProviderPaymentId)
					if err != nil {
						return fmt.Errorf(
							"migrate subscription payment reference %d: %w",
							reference.Id,
							err,
						)
					}
					if reference.PaymentProvider == paymentProvider &&
						reference.ProviderPaymentId == paymentID &&
						reference.PaymentIdentityHash != nil &&
						*reference.PaymentIdentityHash == paymentHash {
						continue
					}
					if err := tx.Model(&SubscriptionProviderPayment{}).
						Where("id = ?", reference.Id).
						UpdateColumns(map[string]any{
							"payment_provider":      paymentProvider,
							"provider_payment_id":   paymentID,
							"payment_identity_hash": paymentHash,
						}).Error; err != nil {
						return fmt.Errorf(
							"migrate subscription payment reference %d: %w",
							reference.Id,
							err,
						)
					}
				}
				return nil
			}).Error; err != nil {
			return err
		}

		var paymentOrders []SubscriptionOrder
		return lockForUpdate(tx).
			Where("provider_payment_id <> ''").
			FindInBatches(&paymentOrders, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range paymentOrders {
					order := &paymentOrders[i]
					paymentProvider := inferLegacyPaymentProvider(
						order.PaymentProvider,
						order.PaymentMethod,
					)
					if order.PaymentProvider != paymentProvider {
						if err := tx.Model(&SubscriptionOrder{}).
							Where("id = ?", order.Id).
							Update("payment_provider", paymentProvider).Error; err != nil {
							return fmt.Errorf(
								"migrate subscription order %d payment provider: %w",
								order.Id,
								err,
							)
						}
					}
					if err := bindSubscriptionProviderPaymentsTx(
						tx,
						paymentProvider,
						order.Id,
						[]string{order.ProviderPaymentId},
					); err != nil {
						return fmt.Errorf(
							"backfill subscription order %d payment reference: %w",
							order.Id,
							err,
						)
					}
				}
				return nil
			}).Error
	})
}

func migrateCasbinRuleHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var rules []CasbinRule
		return lockForUpdate(tx).
			FindInBatches(&rules, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range rules {
					rule := &rules[i]
					if err := rule.BeforeCreate(tx); err != nil {
						return fmt.Errorf("migrate Casbin rule %d: %w", rule.Id, err)
					}
					if err := tx.Model(&CasbinRule{}).Where("id = ?", rule.Id).
						UpdateColumn("rule_hash", rule.RuleHash).Error; err != nil {
						return fmt.Errorf("migrate Casbin rule %d: %w", rule.Id, err)
					}
				}
				return nil
			}).Error
	})
}

func migratePerfMetricHashes() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var metrics []PerfMetric
		return lockForUpdate(tx).
			FindInBatches(&metrics, identityMigrationBatchSize, func(_ *gorm.DB, _ int) error {
				for i := range metrics {
					metric := &metrics[i]
					if err := metric.BeforeCreate(tx); err != nil {
						return fmt.Errorf("migrate performance metric %d: %w", metric.Id, err)
					}
					if err := tx.Model(&PerfMetric{}).Where("id = ?", metric.Id).
						UpdateColumn("identity_hash", metric.IdentityHash).Error; err != nil {
						return fmt.Errorf("migrate performance metric %d: %w", metric.Id, err)
					}
				}
				return nil
			}).Error
	})
}
