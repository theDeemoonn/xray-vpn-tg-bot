package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"xray-vpn-tg-bot/internal/apperrors"
	"xray-vpn-tg-bot/internal/domain"
	"xray-vpn-tg-bot/internal/repository"
	"xray-vpn-tg-bot/internal/xui"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type subscriptionService struct {
	subRepo      repository.SubscriptionRepository
	planRepo     repository.PlanRepository
	serverRepo   repository.ServerRepository
	userService  UserService
	userRepo     repository.UserRepository
	logger       *slog.Logger
	configurator *subscriptionConfigurator
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subRepo repository.SubscriptionRepository,
	planRepo repository.PlanRepository,
	serverRepo repository.ServerRepository,
	userService UserService,
	xuiClientFactory func(server *domain.Server) (*xui.Client, error),
	logger *slog.Logger,
	userRepo repository.UserRepository,
) SubscriptionService {
	configurator := newSubscriptionConfigurator(
		subRepo,
		serverRepo,
		userRepo,
		xuiClientFactory,
		logger,
	)

	return &subscriptionService{
		subRepo:      subRepo,
		planRepo:     planRepo,
		serverRepo:   serverRepo,
		userService:  userService,
		userRepo:     userRepo,
		logger:       logger.With(slog.String("service", "subscription")),
		configurator: configurator,
	}
}

// ActivateSubscription creates or extends a user's subscription record in the database after successful payment.
func (s *subscriptionService) ActivateSubscription(ctx context.Context, userID, planID, paymentID primitive.ObjectID) error {
	s.logger.InfoContext(ctx, "Activating subscription DB record", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during DB activation", slog.String("plan_id", planID.Hex()), slog.String("payment_id", paymentID.Hex()))
			return err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription DB activation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return err
	}

	existingSub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil && !errors.Is(err, apperrors.ErrSubscriptionNotFound) {
		s.logger.ErrorContext(ctx, "Failed to check for existing subscription during activation", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)
	var subToConfigure *domain.Subscription
	var wasExtended bool

	if existingSub != nil {
		// Получаем актуальные данные о подписке прямо из базы
		freshSub, err := s.subRepo.GetByID(ctx, existingSub.ID)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get fresh subscription data", slog.String("sub_id", existingSub.ID.Hex()), slog.Any("error", err))
		} else {
			// Обновляем данные из базы
			existingSub = freshSub
		}

		// Сохраняем состояние сервера и трафика
		existingServerID := existingSub.ServerID
		existingInboundID := existingSub.XuiInboundID
		existingClientUID := existingSub.XuiClientUID
		existingConfigLink := existingSub.ConfigLink
		existingClientUUID := existingSub.XuiClientUUID
		existingTrafficUsed := existingSub.TrafficUsed // Сохраняем использованный трафик

		if existingSub.Status == domain.SubscriptionStatusActive && existingSub.ExpiresAt.After(now) {
			newExpiryDate = existingSub.ExpiresAt.Add(plan.Duration)
			s.logger.InfoContext(ctx, "Extending existing active subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("old_expiry", existingSub.ExpiresAt), slog.Time("new_expiry", newExpiryDate))
			wasExtended = true
		} else {
			s.logger.InfoContext(ctx, "Reactivating/overwriting existing subscription DB record", slog.String("user_id", userID.Hex()), slog.String("sub_id", existingSub.ID.Hex()), slog.Time("new_expiry", newExpiryDate))
		}

		// Обновляем общие поля
		existingSub.PlanID = planID
		existingSub.ExpiresAt = newExpiryDate
		existingSub.Status = domain.SubscriptionStatusActive
		existingSub.ActivatedAt = now
		existingSub.UpdatedAt = now
		existingSub.PaymentID = paymentID

		// Рассчитываем новый лимит трафика и сохраняем использованный
		newPlanTrafficBytes := int64(plan.TrafficGB) // Лимит нового плана в байтах

		if wasExtended {
			// Продление: Суммируем остаток старого трафика с новым лимитом
			oldTrafficLimit := existingSub.TrafficLimit
			remainingOldTraffic := oldTrafficLimit - existingTrafficUsed
			if remainingOldTraffic < 0 {
				remainingOldTraffic = 0 // Не может быть отрицательным остатком
			}
			newTotalTrafficLimit := remainingOldTraffic + newPlanTrafficBytes
			existingSub.TrafficLimit = newTotalTrafficLimit
			existingSub.TrafficUsed = existingTrafficUsed // Сохраняем использованный трафик

			s.logger.InfoContext(ctx, "Calculated extended traffic limit",
				slog.String("sub_id", existingSub.ID.Hex()),
				slog.Int64("old_limit_bytes", oldTrafficLimit),
				slog.Int64("old_used_bytes", existingTrafficUsed),
				slog.Int64("remaining_old_bytes", remainingOldTraffic),
				slog.Int64("new_plan_bytes", newPlanTrafficBytes),
				slog.Int64("new_total_limit_bytes", newTotalTrafficLimit))
		} else {
			// Новая подписка или перезапись старой: Устанавливаем лимит нового плана и сбрасываем счетчик
			existingSub.TrafficLimit = newPlanTrafficBytes
			existingSub.TrafficUsed = 0
			s.logger.InfoContext(ctx, "Set new traffic limit and reset usage",
				slog.String("sub_id", existingSub.ID.Hex()),
				slog.Int64("new_limit_bytes", newPlanTrafficBytes))
		}

		// Восстанавливаем состояние сервера, если это продление
		if wasExtended && !existingServerID.IsZero() {
			existingSub.ServerID = existingServerID
			existingSub.XuiInboundID = existingInboundID
			existingSub.XuiClientUID = existingClientUID
			existingSub.ConfigLink = existingConfigLink
			existingSub.XuiClientUUID = existingClientUUID

			// Добавляем логирование для диагностики
			s.logger.InfoContext(ctx, "Restoring server config after extension",
				slog.String("sub_id", existingSub.ID.Hex()),
				slog.String("server_id", existingServerID.Hex()),
				slog.Int("inbound_id", existingInboundID),
				slog.String("client_uid", existingClientUID),
				slog.String("client_uuid", existingClientUUID))
		} else if wasExtended {
			// Если это продление, но сервер не был настроен, сохраняем предыдущее состояние
			// Ничего не делаем, оставляем существующие (пустые) настройки сервера
			s.logger.InfoContext(ctx, "Extended subscription without server config",
				slog.String("sub_id", existingSub.ID.Hex()),
				slog.Bool("has_server_id", !existingServerID.IsZero()))
		} else {
			// Если это не продление, сбрасываем настройки сервера
			existingSub.ServerID = primitive.NilObjectID
			existingSub.XuiInboundID = 0
			existingSub.XuiClientUID = ""
			existingSub.ConfigLink = ""
			existingSub.XuiClientUUID = ""

			s.logger.InfoContext(ctx, "Reset server config for new subscription",
				slog.String("sub_id", existingSub.ID.Hex()),
				slog.Bool("was_extended", wasExtended))
		}

		// Добавляем логирование ПЕРЕД обновлением в БД
		s.logger.InfoContext(ctx, "Subscription state BEFORE DB update",
			slog.String("sub_id", existingSub.ID.Hex()),
			slog.Bool("has_server_id", !existingSub.ServerID.IsZero()),
			slog.String("server_id", existingSub.ServerID.Hex()),
			slog.Int("inbound_id", existingSub.XuiInboundID),
			slog.String("client_uid", existingSub.XuiClientUID))

		if err := s.subRepo.Update(ctx, existingSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to update existing subscription DB record", slog.String("sub_id", existingSub.ID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "Subscription DB record updated successfully", slog.String("sub_id", existingSub.ID.Hex()))

		// Проверяем, что произошло с подпиской после обновления
		updatedSub, err := s.subRepo.GetByID(ctx, existingSub.ID)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get subscription after update", slog.String("sub_id", existingSub.ID.Hex()), slog.Any("error", err))
			subToConfigure = existingSub
		} else {
			s.logger.InfoContext(ctx, "Subscription state AFTER DB update",
				slog.String("sub_id", updatedSub.ID.Hex()),
				slog.Bool("has_server_id", !updatedSub.ServerID.IsZero()),
				slog.String("server_id", updatedSub.ServerID.Hex()),
				slog.Int("inbound_id", updatedSub.XuiInboundID),
				slog.String("client_uid", updatedSub.XuiClientUID))

			// Используем обновленную версию подписки из БД
			subToConfigure = updatedSub
		}

	} else {
		s.logger.InfoContext(ctx, "Creating new subscription DB record", slog.String("user_id", userID.Hex()), slog.Time("expiry", newExpiryDate))
		newSub := &domain.Subscription{
			ID:           primitive.NewObjectID(),
			UserID:       userID,
			PlanID:       planID,
			Status:       domain.SubscriptionStatusActive,
			ActivatedAt:  now,
			ExpiresAt:    newExpiryDate,
			AutoRenew:    false,
			PaymentID:    paymentID,
			CreatedAt:    now,
			UpdatedAt:    now,
			TrafficLimit: int64(plan.TrafficGB),
			TrafficUsed:  0,
		}
		if err := s.subRepo.Create(ctx, newSub); err != nil {
			s.logger.ErrorContext(ctx, "Failed to create new subscription DB record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
			return err
		}
		s.logger.InfoContext(ctx, "New subscription DB record created successfully", slog.String("sub_id", newSub.ID.Hex()))
		subToConfigure = newSub
	}

	if subToConfigure != nil {
		// Добавляем дополнительное логирование для диагностики
		s.logger.InfoContext(ctx, "Checking subscription configuration state",
			slog.String("sub_id", subToConfigure.ID.Hex()),
			slog.Bool("was_extended", wasExtended),
			slog.Bool("has_server_id", !subToConfigure.ServerID.IsZero()),
			slog.Bool("has_client_uid", subToConfigure.XuiClientUID != ""),
			slog.String("server_id", subToConfigure.ServerID.Hex()),
			slog.String("client_uid", subToConfigure.XuiClientUID))

		// Если подписка уже была сконфигурирована с сервером и была продлена,
		// обновляем параметры на сервере 3x-ui
		if wasExtended && !subToConfigure.ServerID.IsZero() && subToConfigure.XuiClientUID != "" {
			s.logger.InfoContext(ctx, "Updating existing client on 3x-ui server after extension",
				slog.String("sub_id", subToConfigure.ID.Hex()),
				slog.String("server_id", subToConfigure.ServerID.Hex()),
				slog.String("client_uid", subToConfigure.XuiClientUID))

			server, err := s.serverRepo.GetByID(ctx, subToConfigure.ServerID)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to get server details for 3x-ui update",
					slog.String("server_id", subToConfigure.ServerID.Hex()),
					slog.Any("error", err))
				// Продолжаем выполнение даже при ошибке, чтобы подписка была активирована в БД
			} else {
				// Создаем клиент XUI для взаимодействия с сервером
				xuiClient, err := s.configurator.xuiClientFactory(server)
				if err != nil {
					s.logger.ErrorContext(ctx, "Failed to create XUI client",
						slog.String("server_id", server.ID.Hex()),
						slog.Any("error", err))
				} else {
					// Получаем текущие настройки клиента
					clientSettings, err := xuiClient.GetClientSettings(ctx, subToConfigure.XuiInboundID, subToConfigure.XuiClientUID)
					if err != nil {
						s.logger.ErrorContext(ctx, "Failed to get client settings from 3x-ui by email",
							slog.String("client_uid", subToConfigure.XuiClientUID),
							slog.Any("error", err))

						// Пробуем найти клиента по metadata (TelegramID и SubscriptionID)
						user, err := s.userRepo.GetByID(ctx, userID)
						if err == nil && user != nil {
							tgID := strconv.FormatInt(user.TelegramID, 10)
							s.logger.InfoContext(ctx, "Attempting to find client by metadata",
								slog.String("tg_id", tgID),
								slog.String("sub_id", subToConfigure.ID.Hex()))

							foundClient, foundInboundID, err := xuiClient.GetClientsByMetadata(ctx, tgID, subToConfigure.ID.Hex())
							if err == nil && foundClient != nil {
								s.logger.InfoContext(ctx, "Found client by metadata",
									slog.String("email", foundClient.Email),
									slog.Int("inbound_id", foundInboundID))

								// Обновим запись подписки в БД с актуальными данными
								subToConfigure.XuiInboundID = foundInboundID
								subToConfigure.XuiClientUID = foundClient.Email
								if foundClient.UUID != "" {
									subToConfigure.XuiClientUUID = foundClient.UUID
								}

								// Сохраняем обновленные данные в БД
								if err := s.subRepo.Update(ctx, subToConfigure); err != nil {
									s.logger.ErrorContext(ctx, "Failed to update subscription with found client data",
										slog.String("sub_id", subToConfigure.ID.Hex()),
										slog.Any("error", err))
								}

								clientSettings = foundClient
							} else {
								s.logger.ErrorContext(ctx, "Could not find client by metadata",
									slog.String("tg_id", tgID),
									slog.String("sub_id", subToConfigure.ID.Hex()),
									slog.Any("error", err))
							}
						}
					}

					if clientSettings == nil {
						s.logger.ErrorContext(ctx, "Could not get or find client settings, skipping XUI update",
							slog.String("sub_id", subToConfigure.ID.Hex()))
					} else {
						// Обновляем срок действия и трафик
						expiryTimeMillis := subToConfigure.ExpiresAt.UnixMilli()
						// subToConfigure.TrafficLimit уже содержит байты
						trafficLimitBytes := subToConfigure.TrafficLimit

						s.logger.InfoContext(ctx, "Updating client settings in XUI",
							slog.String("email", clientSettings.Email),
							slog.Time("old_expiry", time.UnixMilli(clientSettings.ExpiryTime)),
							slog.Time("new_expiry", subToConfigure.ExpiresAt),
							slog.Int64("old_traffic_bytes", clientSettings.TotalBytes),
							slog.Int64("new_traffic_bytes", trafficLimitBytes))

						clientSettings.ExpiryTime = expiryTimeMillis
						clientSettings.TotalBytes = trafficLimitBytes

						// Получаем ID пользователя Telegram для клиента, если еще не получили
						if clientSettings.TelegramID == "" {
							user, err := s.userRepo.GetByID(ctx, userID)
							if err == nil && user != nil {
								clientSettings.TelegramID = strconv.FormatInt(user.TelegramID, 10)
							}
						}

						// Обновляем значение SubscriptionID
						clientSettings.SubscriptionID = subToConfigure.ID.Hex()

						// Обновляем клиента в XUI
						idField := subToConfigure.XuiClientUUID
						if idField == "" {
							idField = clientSettings.UUID // Используем UUID из настроек, если нет сохраненного
						}

						// Проверяем, что у нас есть все необходимые данные
						if idField == "" {
							s.logger.ErrorContext(ctx, "Missing UUID for XUI client update",
								slog.String("client_uid", subToConfigure.XuiClientUID))
						} else {
							s.logger.InfoContext(ctx, "Sending update to XUI",
								slog.String("inbound_id", strconv.Itoa(subToConfigure.XuiInboundID)),
								slog.String("uuid", idField),
								slog.String("email", clientSettings.Email))

							err = xuiClient.UpdateClient(ctx, subToConfigure.XuiInboundID, idField, *clientSettings)
							if err != nil {
								s.logger.ErrorContext(ctx, "Failed to update client in 3x-ui",
									slog.String("client_uid", subToConfigure.XuiClientUID),
									slog.Any("error", err))

								// Если не удалось обновить, попробуем найти клиента по Email и обновить его
								s.logger.InfoContext(ctx, "Attempting to find and update client by iterating through inbound clients")

								// Получаем все настройки инбаунда
								inbound, err := xuiClient.GetInbound(ctx, subToConfigure.XuiInboundID)
								if err == nil {
									// Ищем клиента с нашим email
									for _, client := range inbound.Clients {
										if client.Email == subToConfigure.XuiClientUID {
											s.logger.InfoContext(ctx, "Found client directly in inbound",
												slog.String("email", client.Email),
												slog.String("uuid", client.UUID))

											// Обновляем настройки
											client.ExpiryTime = expiryTimeMillis
											client.TotalBytes = trafficLimitBytes
											client.SubscriptionID = subToConfigure.ID.Hex()
											if clientSettings.TelegramID != "" {
												client.TelegramID = clientSettings.TelegramID
											}

											// Сохраняем UUID
											if subToConfigure.XuiClientUUID == "" && client.UUID != "" {
												subToConfigure.XuiClientUUID = client.UUID
												if err := s.subRepo.Update(ctx, subToConfigure); err != nil {
													s.logger.ErrorContext(ctx, "Failed to update subscription with UUID",
														slog.String("sub_id", subToConfigure.ID.Hex()),
														slog.Any("error", err))
												}
											}

											// Обновляем клиента
											err = xuiClient.UpdateClient(ctx, subToConfigure.XuiInboundID, client.UUID, client)
											if err != nil {
												s.logger.ErrorContext(ctx, "Failed second attempt to update client",
													slog.String("client_uid", subToConfigure.XuiClientUID),
													slog.Any("error", err))
											} else {
												s.logger.InfoContext(ctx, "Successfully updated client on second attempt",
													slog.String("client_uid", subToConfigure.XuiClientUID),
													slog.Time("new_expiry", subToConfigure.ExpiresAt),
													slog.Int64("traffic_bytes", trafficLimitBytes))
											}
											break
										}
									}
								}
							} else {
								s.logger.InfoContext(ctx, "Successfully updated client in 3x-ui after subscription extension",
									slog.String("client_uid", subToConfigure.XuiClientUID),
									slog.Time("new_expiry", subToConfigure.ExpiresAt),
									slog.Int64("traffic_bytes", trafficLimitBytes))

								// Также обновим данные о сервере в подписке если они изменились
								if subToConfigure.XuiClientUUID != idField {
									subToConfigure.XuiClientUUID = idField
									if err := s.subRepo.Update(ctx, subToConfigure); err != nil {
										s.logger.ErrorContext(ctx, "Failed to update subscription with UUID after XUI update",
											slog.String("sub_id", subToConfigure.ID.Hex()),
											slog.Any("error", err))
									}
								}
							}
						}
					}
				}
			}
		} else if wasExtended && subToConfigure.ServerID.IsZero() {
			// Если это продление подписки, но ServerID пустой, нужно проверить,
			// существует ли уже клиент на сервере с таким email
			s.logger.InfoContext(ctx, "Extended subscription has no server configuration, checking existing clients",
				slog.String("sub_id", subToConfigure.ID.Hex()))

			// Получаем список всех серверов
			servers, err := s.serverRepo.GetAllEnabled(ctx)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to get enabled servers for client check", slog.Any("error", err))
			} else if len(servers) > 0 {
				// Используем первый доступный сервер для поиска
				server := servers[0]

				// Создаем клиент XUI
				xuiClient, err := s.configurator.xuiClientFactory(server)
				if err != nil {
					s.logger.ErrorContext(ctx, "Failed to create XUI client for checking existing clients",
						slog.String("server_id", server.ID.Hex()),
						slog.Any("error", err))
				} else {
					// Попробуем найти клиента по metadata (TelegramID и SubscriptionID)
					user, err := s.userRepo.GetByID(ctx, userID)
					if err == nil && user != nil {
						tgID := strconv.FormatInt(user.TelegramID, 10)
						s.logger.InfoContext(ctx, "Attempting to find client by metadata during renewal",
							slog.String("tg_id", tgID),
							slog.String("sub_id", subToConfigure.ID.Hex()))

						foundClient, foundInboundID, err := xuiClient.GetClientsByMetadata(ctx, tgID, subToConfigure.ID.Hex())
						if err == nil && foundClient != nil {
							s.logger.InfoContext(ctx, "Found existing client by metadata during renewal",
								slog.String("email", foundClient.Email),
								slog.Int("inbound_id", foundInboundID))

							// Обновляем запись подписки в БД с найденными данными
							subToConfigure.ServerID = server.ID
							subToConfigure.XuiInboundID = foundInboundID
							subToConfigure.XuiClientUID = foundClient.Email
							if foundClient.UUID != "" {
								subToConfigure.XuiClientUUID = foundClient.UUID
							}

							// Сохраняем обновленные данные в БД
							if err := s.subRepo.Update(ctx, subToConfigure); err != nil {
								s.logger.ErrorContext(ctx, "Failed to update subscription with found client data during renewal",
									slog.String("sub_id", subToConfigure.ID.Hex()),
									slog.Any("error", err))
							} else {
								s.logger.InfoContext(ctx, "Successfully restored server configuration for extended subscription",
									slog.String("sub_id", subToConfigure.ID.Hex()),
									slog.String("server_id", server.ID.Hex()),
									slog.String("client_email", foundClient.Email))

								// Теперь обновим параметры клиента (срок действия и лимит трафика)
								expiryTimeMillis := subToConfigure.ExpiresAt.UnixMilli()
								// subToConfigure.TrafficLimit уже в байтах
								trafficLimitBytes := subToConfigure.TrafficLimit

								foundClient.ExpiryTime = expiryTimeMillis
								foundClient.TotalBytes = trafficLimitBytes
								foundClient.SubscriptionID = subToConfigure.ID.Hex()

								// Обновляем клиента в XUI
								err = xuiClient.UpdateClient(ctx, foundInboundID, foundClient.UUID, *foundClient)
								if err != nil {
									s.logger.ErrorContext(ctx, "Failed to update client after restoring configuration",
										slog.String("client_uid", foundClient.Email),
										slog.Time("new_expiry", subToConfigure.ExpiresAt),
										slog.Int64("traffic_bytes", trafficLimitBytes))
								} else {
									s.logger.InfoContext(ctx, "Successfully updated client after restoring configuration",
										slog.String("client_uid", foundClient.Email),
										slog.Time("new_expiry", subToConfigure.ExpiresAt),
										slog.Int64("traffic_bytes", trafficLimitBytes))

									// Успешно восстановили конфигурацию, выходим из функции
									s.logger.InfoContext(ctx, "Subscription DB activation/update process completed with restored configuration",
										slog.String("user_id", userID.Hex()),
										slog.String("plan_id", planID.Hex()))
									return nil
								}
							}
						}
					}
				}
			}

			// Если не удалось найти или восстановить - продолжаем как с новой подпиской
			s.logger.InfoContext(ctx, "Could not restore configuration, proceeding with new server setup",
				slog.String("sub_id", subToConfigure.ID.Hex()))
		}

		// Код для автоконфигурации нового сервера остается без изменений
		if subToConfigure.ServerID.IsZero() {
			s.logger.InfoContext(ctx, "Subscription needs server configuration", slog.String("sub_id", subToConfigure.ID.Hex()))
			servers, err := s.serverRepo.GetAllEnabled(ctx)
			if err != nil {
				s.logger.ErrorContext(ctx, "Failed to get enabled servers for auto-configuration", slog.Any("error", err))
				s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but failed to find server. Manual intervention needed.", slog.String("sub_id", subToConfigure.ID.Hex()))
				return nil
			}
			if len(servers) == 0 {
				s.logger.ErrorContext(ctx, "CRITICAL: No enabled servers found for auto-configuration. Subscription activated in DB without server.", slog.String("sub_id", subToConfigure.ID.Hex()))
				return nil
			}
			selectedServer := servers[0]

			s.logger.InfoContext(ctx, "Attempting to auto-configure server for subscription", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
			configErr := s.configurator.ConfigureSubscriptionServer(ctx, userID, subToConfigure.ID, selectedServer.ID)
			if configErr != nil {
				s.logger.ErrorContext(ctx, "CRITICAL: Subscription activated in DB, but auto-configuration failed.", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()), slog.Any("config_error", configErr))
				return nil
			}
			s.logger.InfoContext(ctx, "Auto-configuration successful", slog.String("sub_id", subToConfigure.ID.Hex()), slog.String("server_id", selectedServer.ID.Hex()))
		}
	} else {
		s.logger.ErrorContext(ctx, "Internal logic error: subToConfigure was nil after DB update/create", slog.String("user_id", userID.Hex()))
	}

	s.logger.InfoContext(ctx, "Subscription DB activation/update process completed", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))
	return nil
}

// GetUserActiveSubscription retrieves the active subscription for a user.
func (s *subscriptionService) GetUserActiveSubscription(ctx context.Context, userID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting active subscription for user", slog.String("user_id", userID.Hex()))
	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.DebugContext(ctx, "No active subscription found for user", slog.String("user_id", userID.Hex()))
			return nil, nil
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}
	return sub, nil
}

// GetSubscriptionByID retrieves a subscription by its ID.
func (s *subscriptionService) GetSubscriptionByID(ctx context.Context, subID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.DebugContext(ctx, "Getting subscription by ID", slog.String("sub_id", subID.Hex()))
	sub, err := s.subRepo.GetByID(ctx, subID)
	if err != nil {
		var appErr *apperrors.Error
		if errors.As(err, &appErr) && appErr.Code == apperrors.ErrCodeNotFound {
			s.logger.WarnContext(ctx, "Subscription not found by ID", slog.String("sub_id", subID.Hex()))
			return nil, apperrors.ErrSubscriptionNotFound
		}
		s.logger.ErrorContext(ctx, "Failed to get subscription by ID from repository", slog.String("sub_id", subID.Hex()), slog.Any("error", err))
		return nil, err
	}
	return sub, nil
}

// ConfigureSubscriptionServer delegates to the configurator.
func (s *subscriptionService) ConfigureSubscriptionServer(ctx context.Context, userID, subID, serverID primitive.ObjectID) error {
	return s.configurator.ConfigureSubscriptionServer(ctx, userID, subID, serverID)
}

// GetConfigLink delegates to the configurator.
func (s *subscriptionService) GetConfigLink(ctx context.Context, sub *domain.Subscription) (string, error) {
	return s.configurator.GetConfigLink(ctx, sub)
}

// GetSubscriptionQRCode delegates to the configurator.
func (s *subscriptionService) GetSubscriptionQRCode(ctx context.Context, sub *domain.Subscription) ([]byte, error) {
	return s.configurator.GetSubscriptionQRCode(ctx, sub)
}

// FindAndExpireSubscriptions находит и обновляет статус подписок, которые истекли
func (s *subscriptionService) FindAndExpireSubscriptions(ctx context.Context) error {
	now := time.Now()
	s.logger.InfoContext(ctx, "Проверка и обновление истекших подписок", slog.Time("now", now))

	filter := bson.M{
		"status": domain.SubscriptionStatusActive,
		"expires_at": bson.M{
			"$lt": now,
		},
		"auto_renew": false,
	}

	subscriptions, err := s.subRepo.FindByFilter(ctx, filter)
	if err != nil {
		s.logger.ErrorContext(ctx, "Ошибка при поиске истекших подписок", slog.Any("error", err))
		return err
	}

	s.logger.InfoContext(ctx, "Найдены истекшие подписки", slog.Int("count", len(subscriptions)))

	for _, sub := range subscriptions {
		oldStatus := sub.Status
		sub.Status = domain.SubscriptionStatusExpired
		sub.UpdatedAt = now

		if err := s.subRepo.Update(ctx, sub); err != nil {
			s.logger.ErrorContext(ctx, "Ошибка при обновлении статуса подписки",
				slog.String("sub_id", sub.ID.Hex()),
				slog.String("old_status", string(oldStatus)),
				slog.String("new_status", string(sub.Status)),
				slog.Any("error", err))
			continue
		}

		s.logger.InfoContext(ctx, "Подписка успешно помечена как истекшая",
			slog.String("sub_id", sub.ID.Hex()),
			slog.String("user_id", sub.UserID.Hex()))
	}

	return nil
}

// CreateSubscription создает новую подписку для пользователя
func (s *subscriptionService) CreateSubscription(ctx context.Context, userID primitive.ObjectID, planID primitive.ObjectID) (*domain.Subscription, error) {
	s.logger.InfoContext(ctx, "Creating new subscription", slog.String("user_id", userID.Hex()), slog.String("plan_id", planID.Hex()))

	plan, err := s.planRepo.GetByID(ctx, planID)
	if err != nil {
		if errors.Is(err, apperrors.ErrPlanNotFound) {
			s.logger.ErrorContext(ctx, "Plan not found during subscription creation", slog.String("plan_id", planID.Hex()))
			return nil, err
		}
		s.logger.ErrorContext(ctx, "Failed to get plan details for subscription creation", slog.String("plan_id", planID.Hex()), slog.Any("error", err))
		return nil, err
	}

	now := time.Now()
	newExpiryDate := now.Add(plan.Duration)

	newSub := &domain.Subscription{
		ID:           primitive.NewObjectID(),
		UserID:       userID,
		PlanID:       planID,
		Status:       domain.SubscriptionStatusPending,
		ActivatedAt:  now,
		ExpiresAt:    newExpiryDate,
		AutoRenew:    false,
		CreatedAt:    now,
		UpdatedAt:    now,
		TrafficLimit: int64(plan.TrafficGB),
		TrafficUsed:  0,
	}

	if err := s.subRepo.Create(ctx, newSub); err != nil {
		s.logger.ErrorContext(ctx, "Failed to create new subscription record", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, err
	}

	s.logger.InfoContext(ctx, "New subscription created successfully (needs configuration)", slog.String("sub_id", newSub.ID.Hex()))
	return newSub, nil
}

// GetActiveSubscriptionForUser получает список активных подписок для пользователя
func (s *subscriptionService) GetActiveSubscriptionForUser(ctx context.Context, userID primitive.ObjectID) ([]*domain.SubscriptionDetails, error) {
	s.logger.DebugContext(ctx, "Getting active subscriptions for user", slog.String("user_id", userID.Hex()))

	sub, err := s.subRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, apperrors.ErrSubscriptionNotFound) {
			s.logger.DebugContext(ctx, "No active subscriptions found for user", slog.String("user_id", userID.Hex()))
			return []*domain.SubscriptionDetails{}, nil
		}
		s.logger.ErrorContext(ctx, "Failed to get active subscription from repository", slog.String("user_id", userID.Hex()), slog.Any("error", err))
		return nil, fmt.Errorf("failed to retrieve active subscription: %w", err)
	}

	if sub != nil {
		plan, err := s.planRepo.GetByID(ctx, sub.PlanID)
		if err != nil {
			s.logger.ErrorContext(ctx, "Failed to get plan for subscription details", slog.String("sub_id", sub.ID.Hex()), slog.String("plan_id", sub.PlanID.Hex()), slog.Any("error", err))
		}

		var serverName string
		if !sub.ServerID.IsZero() {
			server, err := s.serverRepo.GetByID(ctx, sub.ServerID)
			if err == nil && server != nil {
				serverName = server.Name
			} else if err != nil {
				s.logger.WarnContext(ctx, "Failed to get server name for subscription details", slog.String("sub_id", sub.ID.Hex()), slog.String("server_id", sub.ServerID.Hex()), slog.Any("error", err))
			}
		}

		details := &domain.SubscriptionDetails{
			Subscription: sub,
			PlanName:     "Неизвестный план",
			ServerName:   serverName,
		}

		if plan != nil {
			details.PlanName = plan.Name
		}

		return []*domain.SubscriptionDetails{details}, nil
	}

	return []*domain.SubscriptionDetails{}, nil
}

// UpdateTrafficStats обновляет информацию о трафике и дате окончания подписки
func (s *subscriptionService) UpdateTrafficStats(ctx context.Context, sub *domain.Subscription) error {
	s.logger.InfoContext(ctx, "Updating subscription traffic statistics",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int64("traffic_used", sub.TrafficUsed),
		slog.Int64("traffic_limit", sub.TrafficLimit),
		slog.Time("expires_at", sub.ExpiresAt))

	// Обновляем время последнего изменения
	sub.UpdatedAt = time.Now()

	// Сохраняем изменения в базе данных
	err := s.subRepo.Update(ctx, sub)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to update subscription traffic stats in repository",
			slog.String("sub_id", sub.ID.Hex()),
			slog.Any("error", err))
		return err
	}

	s.logger.Debug("Subscription traffic statistics updated successfully",
		slog.String("sub_id", sub.ID.Hex()),
		slog.Int64("traffic_used", sub.TrafficUsed))
	return nil
}
