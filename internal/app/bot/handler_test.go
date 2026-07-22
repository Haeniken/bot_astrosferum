package bot

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
	"bot_astrosferum/internal/store"
)

func TestForecastInputsShareOneImmutableRun(t *testing.T) {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	// Synthetic fixtures describe the same reference point, but this helper
	// intentionally compares identity rather than assuming fixture metadata.
	surface.Provider, surface.RunID, surface.BaseTime = vertical.Provider, vertical.RunID, vertical.BaseTime
	cloud.Provider, cloud.RunID, cloud.BaseTime = vertical.Provider, vertical.RunID, vertical.BaseTime
	tests := []struct {
		name       string
		surface    forecast.SurfaceSeries
		surfaceErr error
		cloud      forecast.CloudSeries
		cloudErr   error
		want       bool
	}{
		{name: "same run", surface: surface, cloud: cloud, want: true},
		{name: "surface provider", surface: func() forecast.SurfaceSeries { value := surface; value.Provider = "other"; return value }(), cloud: cloud},
		{name: "surface run", surface: func() forecast.SurfaceSeries { value := surface; value.RunID = "2026072218"; return value }(), cloud: cloud},
		{name: "surface base time", surface: func() forecast.SurfaceSeries {
			value := surface
			value.BaseTime = value.BaseTime.Add(6 * time.Hour)
			return value
		}(), cloud: cloud},
		{name: "cloud provider", surface: surface, cloud: func() forecast.CloudSeries { value := cloud; value.Provider = "other"; return value }()},
		{name: "cloud run", surface: surface, cloud: func() forecast.CloudSeries { value := cloud; value.RunID = "2026072218"; return value }()},
		{name: "cloud base time", surface: surface, cloud: func() forecast.CloudSeries {
			value := cloud
			value.BaseTime = value.BaseTime.Add(6 * time.Hour)
			return value
		}()},
		{name: "failed surface is ignored", surfaceErr: errors.New("surface unavailable"), cloud: cloud, want: true},
		{name: "failed cloud is ignored", surface: surface, cloudErr: errors.New("cloud unavailable"), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := forecastInputsShareRun(vertical, test.surface, test.surfaceErr, test.cloud, test.cloudErr); got != test.want {
				t.Fatalf("forecastInputsShareRun = %t, want %t", got, test.want)
			}
		})
	}
}

type sentMessage struct {
	chatID         int64
	text           string
	locationButton bool
}

type fakeMessenger struct {
	messages  []sentMessage
	photos    []string
	documents []string
}

type fakeActionMessenger struct {
	fakeMessenger
	actionMessages []ActionKeyboard
	actionTexts    []string
	answers        []string
}

type fixedForecastProvider struct {
	vertical forecast.VerticalSeries
	surface  forecast.SurfaceSeries
	cloud    forecast.CloudSeries
}

type blockingTouchPersistence struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingTouchPersistence) TouchUser(context.Context, int64) error {
	close(p.entered)
	<-p.release
	return nil
}

func (*blockingTouchPersistence) SavePoint(context.Context, int64, string, float64, float64) error {
	return nil
}

func (*blockingTouchPersistence) Points(context.Context, int64) ([]store.Point, error) {
	return nil, nil
}

func (*blockingTouchPersistence) DeletePoint(context.Context, int64, int64) (bool, error) {
	return false, nil
}

func (*blockingTouchPersistence) RecordForecast(context.Context, int64, bool) error {
	return nil
}

func (*blockingTouchPersistence) Stats(context.Context) (int64, []store.DailyUsage, error) {
	return 0, nil, nil
}

func (provider fixedForecastProvider) Vertical(context.Context, forecast.Location) (forecast.VerticalSeries, error) {
	return provider.vertical, nil
}

func (provider fixedForecastProvider) Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error) {
	return provider.surface, nil
}

func (provider fixedForecastProvider) Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error) {
	return provider.cloud, nil
}

func (messenger *fakeActionMessenger) SendMessageWithActions(_ context.Context, _ int64, text string, keyboard ActionKeyboard) error {
	messenger.actionTexts = append(messenger.actionTexts, text)
	messenger.actionMessages = append(messenger.actionMessages, keyboard)
	return nil
}

func (messenger *fakeActionMessenger) AnswerAction(_ context.Context, token, text string) error {
	messenger.answers = append(messenger.answers, token+":"+text)
	return nil
}

func (messenger *fakeMessenger) SendPhoto(_ context.Context, _ int64, path, _ string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	messenger.photos = append(messenger.photos, path)
	return nil
}

func (messenger *fakeMessenger) SendDocument(_ context.Context, _ int64, path, _ string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	messenger.documents = append(messenger.documents, path)
	return nil
}

func (messenger *fakeMessenger) SendMessage(_ context.Context, chatID int64, text string, locationButton bool) error {
	messenger.messages = append(messenger.messages, sentMessage{chatID: chatID, text: text, locationButton: locationButton})
	return nil
}

func TestStartProvidesUsageAndInterpretation(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	err = handler.Handle(context.Background(), Update{Message: &Message{Chat: Chat{ID: 42}, From: &User{ID: 42, LanguageCode: "ru"}, Text: "/start"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !messenger.messages[0].locationButton {
		t.Fatalf("unexpected messages: %#v", messenger.messages)
	}
	for _, expected := range []string{
		"/forecast 59.9386 30.3141", "Forecast Wind Seeing Index", "1…10", "часовой зоне",
		"по горизонтали", "сдвиг", "облачность не меняет wind-based seeing", "850 hPa ≈ 1,5 км",
		"нижние", "средние", "верхние", "длинные выдержки", "не означает плохой сиинг",
		"ICON (TKE до динамической MH 500–2000 м AGL + HMNSP99 выше)", "MH — почасовая", "τ₀", "f/F", "в Overall Index она не входит",
	} {
		if !strings.Contains(messenger.messages[0].text, expected) {
			t.Fatalf("help does not contain %q", expected)
		}
	}
	if len([]byte(messenger.messages[0].text)) > 4096 {
		t.Fatalf("Russian help is too long: %d bytes", len([]byte(messenger.messages[0].text)))
	}
	if strings.Contains(messenger.messages[0].text, "«Горизонт»") {
		t.Fatal("disabled Horizon was advertised in /start")
	}
}

func TestNonRussianClientReceivesEnglishHelp(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), Update{Message: &Message{Chat: Chat{ID: 42}, From: &User{ID: 42, LanguageCode: "de"}, Text: "/start"}}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "How to request a forecast") {
		t.Fatalf("unexpected English reply: %#v", messenger.messages)
	}
	if strings.Contains(messenger.messages[0].text, "Как читать результат") {
		t.Fatalf("English reply contains Russian help: %q", messenger.messages[0].text)
	}
	if len([]byte(messenger.messages[0].text)) > 4096 {
		t.Fatalf("English help is too long: %d bytes", len([]byte(messenger.messages[0].text)))
	}
}

func TestMissingLanguageCodeUsesEnglish(t *testing.T) {
	if got := languageFromCode(""); got != languageEnglish {
		t.Fatalf("languageFromCode(empty) = %q, want en", got)
	}
	if got := languageFromCode("ru-RU"); got != languageRussian {
		t.Fatalf("languageFromCode(ru-RU) = %q, want ru", got)
	}
}

func TestNativeLocationResolvesCoordinateTimezone(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	err = handler.Handle(context.Background(), Update{Message: &Message{
		Chat: Chat{ID: 42}, From: &User{ID: 42, LanguageCode: "ru"}, Location: &Location{Latitude: 59.9386, Longitude: 30.3141},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "Europe/Moscow · MSK (UTC+3)") {
		t.Fatalf("unexpected reply: %#v", messenger.messages)
	}
}

func TestForecastCommandValidationError(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), Update{Message: &Message{Chat: Chat{ID: 42}, From: &User{ID: 42, LanguageCode: "ru"}, Text: "/forecast nope"}}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "Не удалось") {
		t.Fatalf("unexpected reply: %#v", messenger.messages)
	}
}

func TestForecastFreshnessText(t *testing.T) {
	now := time.Date(2026, time.July, 21, 15, 30, 0, 0, time.UTC)

	fresh := forecastFreshnessText(now.Add(-9*time.Hour-17*time.Minute), now, 12*time.Hour, languageRussian)
	for _, expected := range []string{"Актуальность данных:", "9 ч 17 мин"} {
		if !strings.Contains(fresh, expected) {
			t.Fatalf("fresh status %q does not contain %q", fresh, expected)
		}
	}

	stale := forecastFreshnessText(now.Add(-14*time.Hour-2*time.Minute), now, 12*time.Hour, languageRussian)
	for _, expected := range []string{"⚠️", "Данные устарели (stale run)", "14 ч 2 мин", "порог 12 ч 0 мин", "последние изменения атмосферы"} {
		if !strings.Contains(stale, expected) {
			t.Fatalf("stale status %q does not contain %q", stale, expected)
		}
	}
}

func TestForecastFreshnessTreatsClockSkewAsZeroAge(t *testing.T) {
	now := time.Date(2026, time.July, 21, 15, 30, 0, 0, time.UTC)
	status := forecastFreshnessText(now.Add(time.Minute), now, 12*time.Hour, languageRussian)
	if status != "Актуальность данных: 0 мин" {
		t.Fatalf("unexpected clock-skew status: %q", status)
	}
}

func TestForecastFreshnessEnglish(t *testing.T) {
	now := time.Date(2026, time.July, 21, 15, 30, 0, 0, time.UTC)
	status := forecastFreshnessText(now.Add(-13*time.Hour-5*time.Minute), now, 12*time.Hour, languageEnglish)
	for _, expected := range []string{"⚠️ Stale run", "13h 5min", "12h 0min", "recent atmospheric changes"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("English freshness status %q does not contain %q", status, expected)
		}
	}
}

func TestHandlerRoutesActionBeforeMessageFlow(t *testing.T) {
	messenger := &fakeActionMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := handler.EnableActions(ActionRouter{
		"test.v1": func(_ context.Context, invocation ActionInvocation, payload string) error {
			called = invocation.Chat.ID == 42 && invocation.From.ID == 7 && payload == "payload"
			return messenger.AnswerAction(context.Background(), invocation.Token, "accepted")
		},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := EncodeAction("test.v1", "payload")
	if err != nil {
		t.Fatal(err)
	}
	update := Update{Action: &ActionInvocation{Token: "token", Data: data, Chat: Chat{ID: 42}, From: &User{ID: 7, LanguageCode: "en"}}}
	if err := handler.Handle(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if !called || len(messenger.answers) != 1 || messenger.answers[0] != "token:accepted" {
		t.Fatalf("called=%t answers=%v", called, messenger.answers)
	}
}

func TestHandlerAcknowledgesActionBeforeSlowPersistence(t *testing.T) {
	messenger := &fakeActionMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	persistence := &blockingTouchPersistence{entered: make(chan struct{}), release: make(chan struct{})}
	if err := handler.EnablePersistence(persistence, nil); err != nil {
		t.Fatal(err)
	}
	if err := handler.EnableActions(ActionRouter{
		"ack.v1": func(ctx context.Context, invocation ActionInvocation, _ string) error {
			return messenger.AnswerAction(ctx, invocation.Token, "accepted")
		},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := EncodeAction("ack.v1", "payload")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- handler.Handle(context.Background(), Update{Action: &ActionInvocation{
			Token: "token", Data: data, Chat: Chat{ID: 42}, From: &User{ID: 7, LanguageCode: "en"},
		}})
	}()
	select {
	case <-persistence.entered:
	case <-time.After(time.Second):
		t.Fatal("action did not reach persistence")
	}
	if len(messenger.answers) != 1 || messenger.answers[0] != "token:accepted" {
		t.Fatalf("callback was not acknowledged before persistence: %v", messenger.answers)
	}
	close(persistence.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHandlerAcknowledgesUnknownActionWithoutExposingInternals(t *testing.T) {
	messenger := &fakeActionMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeAction("missing.v1", "secret-payload")
	if err != nil {
		t.Fatal(err)
	}
	update := Update{Action: &ActionInvocation{Token: "token", Data: data, Chat: Chat{ID: 42}, From: &User{ID: 7, LanguageCode: "ru"}}}
	if err := handler.Handle(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if len(messenger.answers) != 1 || !strings.Contains(messenger.answers[0], "Кнопка устарела") || strings.Contains(messenger.answers[0], "secret-payload") {
		t.Fatalf("answers=%v", messenger.answers)
	}
}

func TestHandlerOffersFullPeriodHorizonOnlyForICONEU(t *testing.T) {
	messenger := &fakeActionMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	if err := handler.EnableHorizon("telegram", jobs); err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), Update{Message: &Message{
		Chat: Chat{ID: 42}, From: &User{ID: 42, LanguageCode: "ru"}, Text: "/start",
	}}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "8. «Горизонт»") {
		t.Fatalf("enabled Horizon missing from /start: %#v", messenger.messages)
	}
	location := forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	handler.offerHorizon(context.Background(), 42, 1, "icon-global", horizonTestRunID, location, 17, languageRussian)
	if len(messenger.actionMessages) != 0 {
		t.Fatal("ICON Global forecast exposed a Horizon action")
	}
	handler.offerHorizon(context.Background(), 42, 2, HorizonProviderICONEU, horizonTestRunID, location, 17, languageRussian)
	if len(messenger.actionMessages) != 1 || len(messenger.actionMessages[0]) != 1 || len(messenger.actionMessages[0][0]) != 1 {
		t.Fatalf("unexpected Horizon keyboard: %#v", messenger.actionMessages)
	}
	if len(messenger.actionTexts) != 1 || !strings.Contains(messenger.actionTexts[0], "73 почасовых срока") ||
		!strings.Contains(messenger.actionTexts[0], "f000…f072") {
		t.Fatalf("unexpected Horizon prompt: %#v", messenger.actionTexts)
	}
	for _, forbidden := range []string{"дерев", "здани", "локальн"} {
		if strings.Contains(strings.ToLower(messenger.actionTexts[0]), forbidden) {
			t.Fatalf("Horizon prompt contains forbidden local-obstacle wording %q", forbidden)
		}
	}
}

func TestHandlerGlobalForecastCompletesWithoutExposingOrCallingHorizon(t *testing.T) {
	messenger := &fakeActionMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	provider := currentGlobalForecastFixture()
	if err := handler.EnableForecast(provider, t.TempDir(), render.Options{Width: 3200, Height: 960, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	source := &horizonFakeSource{currentRun: provider.vertical.RunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	if err := handler.EnableHorizon("telegram", jobs); err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), Update{Message: &Message{
		Chat: Chat{ID: 501}, From: &User{ID: 501, LanguageCode: "en"}, Text: "/forecast 59.9386 30.3141",
	}}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.photos)+len(messenger.documents) == 0 {
		t.Fatal("ordinary ICON Global forecast delivered no charts")
	}
	if len(messenger.actionMessages) != 0 {
		t.Fatal("ordinary ICON Global forecast exposed a Horizon button")
	}
	supportsCalls, currentCalls, seriesCalls := source.horizonCalls()
	if supportsCalls != 0 || currentCalls != 0 || seriesCalls != 0 {
		t.Fatalf("ICON Global forecast touched Horizon source: supports=%d current=%d series=%d", supportsCalls, currentCalls, seriesCalls)
	}
}

func currentGlobalForecastFixture() fixedForecastProvider {
	vertical := forecast.SyntheticVerticalFixture()
	surface := forecast.SyntheticSurfaceFixture()
	cloud := forecast.SyntheticCloudFixture()
	oldBase := vertical.BaseTime
	base := time.Now().UTC().Truncate(6 * time.Hour).Add(-6 * time.Hour)
	delta := base.Sub(oldBase)
	runID := base.Format("2006010215")
	vertical.Provider, vertical.Product, vertical.RunID, vertical.BaseTime = "icon-global", "ICON Global", runID, base
	vertical.GeneratedAt = time.Now().UTC()
	for index := range vertical.Frames {
		vertical.Frames[index].ValidAt = vertical.Frames[index].ValidAt.Add(delta)
	}
	surface.Provider, surface.Product, surface.RunID, surface.BaseTime = "icon-global", "ICON Global", runID, base
	surface.GeneratedAt = time.Now().UTC()
	for index := range surface.Frames {
		surface.Frames[index].ValidAt = surface.Frames[index].ValidAt.Add(delta)
	}
	cloud.Provider, cloud.Product, cloud.RunID, cloud.BaseTime = "icon-global", "ICON Global", runID, base
	cloud.GeneratedAt = time.Now().UTC()
	for index := range cloud.Frames {
		cloud.Frames[index].ValidAt = cloud.Frames[index].ValidAt.Add(delta)
	}
	return fixedForecastProvider{vertical: vertical, surface: surface, cloud: cloud}
}
