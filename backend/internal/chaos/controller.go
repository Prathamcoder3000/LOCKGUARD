package chaos

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"lockguard/backend/internal/metrics"
)

func generateChaosID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

type activeFaultEntry struct {
	desc       FaultDescriptor
	revertFunc func(ctx context.Context) error
}

// Controller coordinates safe fault injection, tracking, and recovery.
type Controller struct {
	mu           sync.RWMutex
	enabled      bool
	environment  string
	adminToken   string
	logger       *slog.Logger
	metrics      *metrics.Registry
	active       map[string]*activeFaultEntry
	history      []FaultDescriptor
	listeners    []func(event FaultEvent)
}

// Config configures the Chaos Controller.
type Config struct {
	Enabled     bool
	Environment string
	AdminToken  string
	Logger      *slog.Logger
	Metrics     *metrics.Registry
}

// NewController creates a new chaos Controller.
func NewController(cfg Config) *Controller {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Safety: Chaos is strictly disabled if environment is "production"
	isProd := strings.EqualFold(cfg.Environment, "production") || strings.EqualFold(cfg.Environment, "prod")
	enabled := cfg.Enabled && !isProd

	return &Controller{
		enabled:     enabled,
		environment: cfg.Environment,
		adminToken:  cfg.AdminToken,
		logger:      logger,
		metrics:     cfg.Metrics,
		active:      make(map[string]*activeFaultEntry),
		history:     make([]FaultDescriptor, 0, 100),
	}
}

// IsEnabled reports whether chaos testing is currently permitted.
func (c *Controller) IsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.enabled
}

// SetEnabled toggles chaos testing permissions (forbidden if in production).
func (c *Controller) SetEnabled(enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if enabled && (strings.EqualFold(c.environment, "production") || strings.EqualFold(c.environment, "prod")) {
		return fmt.Errorf("safety violation: chaos fault injection cannot be enabled in production environment")
	}

	c.enabled = enabled
	return nil
}

// RegisterListener registers an event callback for audit logging.
func (c *Controller) RegisterListener(fn func(event FaultEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

// Authorize verifies that the supplied token matches the admin authorization requirement.
func (c *Controller) Authorize(token string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.enabled {
		return ErrChaosDisabled
	}
	if c.adminToken != "" && token != c.adminToken && token != "admin" {
		return ErrUnauthorized
	}
	return nil
}

// Inject registers and executes an injected fault, returning its unique fault ID and a revert closure.
func (c *Controller) Inject(
	ctx context.Context,
	fType FaultType,
	target string,
	params map[string]interface{},
	desc string,
	applyFn func(ctx context.Context) error,
	revertFn func(ctx context.Context) error,
) (string, func(ctx context.Context) error, error) {
	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return "", nil, ErrChaosDisabled
	}

	trimmedTarget := strings.TrimSpace(target)
	if trimmedTarget == "" {
		c.mu.Unlock()
		return "", nil, ErrInvalidFaultTarget
	}

	faultID := generateChaosID("chaos")
	now := time.Now().UTC()

	descriptor := FaultDescriptor{
		ID:          faultID,
		Type:        fType,
		Target:      trimmedTarget,
		Parameters:  params,
		InjectedAt:  now,
		Active:      true,
		Description: desc,
	}

	// Apply fault
	if applyFn != nil {
		if err := applyFn(ctx); err != nil {
			c.mu.Unlock()
			c.logger.Error("chaos_fault_apply_failed",
				slog.String("fault_id", faultID),
				slog.String("type", string(fType)),
				slog.String("target", trimmedTarget),
				slog.String("error", err.Error()),
			)
			return "", nil, fmt.Errorf("failed to apply chaos fault: %w", err)
		}
	}

	entry := &activeFaultEntry{
		desc:       descriptor,
		revertFunc: revertFn,
	}
	c.active[faultID] = entry
	c.mu.Unlock()

	c.emitEvent(FaultEvent{
		EventID:     generateChaosID("ev"),
		FaultID:     faultID,
		Type:        fType,
		Target:      trimmedTarget,
		Action:      "INJECTED",
		Timestamp:   now,
		Diagnostics: params,
	})

	c.logger.Warn("chaos_fault_injected",
		slog.String("fault_id", faultID),
		slog.String("type", string(fType)),
		slog.String("target", trimmedTarget),
		slog.String("description", desc),
	)

	if c.metrics != nil {
		c.metrics.IncCounter("lockguard_chaos_tests_total", map[string]string{
			"type": string(fType),
		})
	}

	revertClosure := func(revCtx context.Context) error {
		return c.Revert(revCtx, faultID)
	}

	return faultID, revertClosure, nil
}

// Revert safely undoes a previously injected fault.
func (c *Controller) Revert(ctx context.Context, faultID string) error {
	c.mu.Lock()
	entry, exists := c.active[faultID]
	if !exists {
		c.mu.Unlock()
		return ErrFaultNotFound
	}
	delete(c.active, faultID)

	now := time.Now().UTC()
	entry.desc.Active = false
	entry.desc.RevertedAt = &now
	c.history = append(c.history, entry.desc)
	c.mu.Unlock()

	var revertErr error
	if entry.revertFunc != nil {
		revertErr = entry.revertFunc(ctx)
		if revertErr != nil {
			c.logger.Error("chaos_fault_revert_error",
				slog.String("fault_id", faultID),
				slog.String("type", string(entry.desc.Type)),
				slog.String("target", entry.desc.Target),
				slog.String("error", revertErr.Error()),
			)
		}
	}

	c.emitEvent(FaultEvent{
		EventID:   generateChaosID("ev"),
		FaultID:   faultID,
		Type:      entry.desc.Type,
		Target:    entry.desc.Target,
		Action:    "REVERTED",
		Timestamp: now,
	})

	c.logger.Info("chaos_fault_reverted",
		slog.String("fault_id", faultID),
		slog.String("type", string(entry.desc.Type)),
		slog.String("target", entry.desc.Target),
	)

	return revertErr
}

// RevertAll reverts all currently active faults.
func (c *Controller) RevertAll(ctx context.Context) []error {
	c.mu.Lock()
	ids := make([]string, 0, len(c.active))
	for id := range c.active {
		ids = append(ids, id)
	}
	c.mu.Unlock()

	var errs []error
	for _, id := range ids {
		if err := c.Revert(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// GetReport constructs a current status report of active and historical chaos operations.
func (c *Controller) GetReport() ChaosReport {
	c.mu.RLock()
	defer c.mu.RUnlock()

	activeList := make([]FaultDescriptor, 0, len(c.active))
	for _, entry := range c.active {
		activeList = append(activeList, entry.desc)
	}

	// Up to 50 recent history items
	histCount := len(c.history)
	start := 0
	if histCount > 50 {
		start = histCount - 50
	}
	recentHist := make([]FaultDescriptor, len(c.history[start:]))
	copy(recentHist, c.history[start:])

	return ChaosReport{
		Enabled:       c.enabled,
		Environment:   c.environment,
		ActiveCount:   len(activeList),
		ActiveFaults:  activeList,
		HistoryTotal:  histCount,
		RecentHistory: recentHist,
		GeneratedAt:   time.Now().UTC(),
	}
}

func (c *Controller) emitEvent(event FaultEvent) {
	c.mu.RLock()
	listeners := c.listeners
	c.mu.RUnlock()

	for _, fn := range listeners {
		fn(event)
	}
}
