package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"xray-vpn-tg-bot/internal/service"
)

// SubscriptionWorker представляет фоновый воркер для обработки подписок
type SubscriptionWorker struct {
	subscriptionService service.SubscriptionService
	logger              *slog.Logger
	interval            time.Duration
	stopCh              chan struct{}
	wg                  sync.WaitGroup
}

// NewSubscriptionWorker создает новый экземпляр воркера для обработки подписок
func NewSubscriptionWorker(
	subscriptionService service.SubscriptionService,
	logger *slog.Logger,
	interval time.Duration,
) *SubscriptionWorker {
	return &SubscriptionWorker{
		subscriptionService: subscriptionService,
		logger:              logger.With(slog.String("component", "subscription_worker")),
		interval:            interval,
		stopCh:              make(chan struct{}),
	}
}

// Start запускает фоновый процесс проверки подписок
func (w *SubscriptionWorker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.logger.Info("Запуск воркера для обработки подписок", slog.Duration("interval", w.interval))

		// Запускаем проверку сразу при старте
		w.processExpiredSubscriptions(ctx)

		// Создаем таймер для периодической проверки
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				w.processExpiredSubscriptions(ctx)
			case <-w.stopCh:
				w.logger.Info("Остановка воркера обработки подписок...")
				return
			case <-ctx.Done():
				w.logger.Info("Остановка воркера обработки подписок (контекст отменен)...")
				return
			}
		}
	}()
}

// Stop останавливает воркер
func (w *SubscriptionWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
	w.logger.Info("Воркер обработки подписок остановлен")
}

// processExpiredSubscriptions обрабатывает истекшие подписки
func (w *SubscriptionWorker) processExpiredSubscriptions(ctx context.Context) {
	w.logger.Info("Запуск процесса обработки истекших подписок")

	// Создаем локальный контекст с таймаутом для избежания зависаний
	processingCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Вызываем метод сервиса для обработки истекших подписок
	if err := w.subscriptionService.FindAndExpireSubscriptions(processingCtx); err != nil {
		w.logger.Error("Ошибка при обработке истекших подписок", slog.Any("error", err))
	} else {
		w.logger.Info("Обработка истекших подписок успешно завершена")
	}
}
