package memory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kayushkin/bus"
	"github.com/kayushkin/bus/messages"
)

// NATSStore implements MemoryStore by communicating with agent-store over NATS
type NATSStore struct {
	client  *bus.Client
	timeout time.Duration
}

// NewNATSStore creates a new NATS-based memory store client
func NewNATSStore(busClient *bus.Client, timeout time.Duration) MemoryStore {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &NATSStore{
		client:  busClient,
		timeout: timeout,
	}
}

// Helper function to convert Memory to messages.Memory
func (n *NATSStore) memoryToMessage(m Memory) messages.Memory {
	return messages.Memory{
		ID:           m.ID,
		Content:      m.Content,
		Summary:      m.Summary,
		OriginalID:   m.OriginalID,
		Tags:         m.Tags,
		Importance:   m.Importance,
		AccessCount:  m.AccessCount,
		LastAccessed: m.LastAccessed,
		CreatedAt:    m.CreatedAt,
		Source:       m.Source,
		Embedding:    m.Embedding,
		AlwaysLoad:   m.AlwaysLoad,
		ExpiresAt:    m.ExpiresAt,
		Tokens:       m.Tokens,
		RefType:      m.RefType,
		RefTarget:    m.RefTarget,
		IsLazy:       m.IsLazy,
	}
}

// Helper function to convert messages.Memory to Memory
func (n *NATSStore) messageToMemory(m messages.Memory) Memory {
	return Memory{
		ID:           m.ID,
		Content:      m.Content,
		Summary:      m.Summary,
		OriginalID:   m.OriginalID,
		Tags:         m.Tags,
		Importance:   m.Importance,
		AccessCount:  m.AccessCount,
		LastAccessed: m.LastAccessed,
		CreatedAt:    m.CreatedAt,
		Source:       m.Source,
		Embedding:    m.Embedding,
		AlwaysLoad:   m.AlwaysLoad,
		ExpiresAt:    m.ExpiresAt,
		Tokens:       m.Tokens,
		RefType:      m.RefType,
		RefTarget:    m.RefTarget,
		IsLazy:       m.IsLazy,
	}
}

// Save stores a new memory
func (n *NATSStore) Save(m Memory) error {
	req := messages.MemorySaveRequest{Memory: n.memoryToMessage(m)}
	
	respData, err := n.client.Request("memory.save", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemorySaveResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("save failed: %s", resp.Error)
	}
	
	return nil
}

// Get retrieves a memory by ID
func (n *NATSStore) Get(id string) (*Memory, error) {
	req := messages.MemoryGetRequest{ID: id}
	
	respData, err := n.client.Request("memory.get", req, n.timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryGetResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	
	if resp.Error != "" {
		return nil, fmt.Errorf("get failed: %s", resp.Error)
	}
	
	if resp.Memory == nil {
		return nil, fmt.Errorf("memory not found: %s", id)
	}
	
	m := n.messageToMemory(*resp.Memory)
	return &m, nil
}

// Search finds memories matching the query
func (n *NATSStore) Search(query string, limit int) ([]Memory, error) {
	req := messages.MemorySearchRequest{Query: query, Limit: limit}
	
	respData, err := n.client.Request("memory.search", req, n.timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemorySearchResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	
	if resp.Error != "" {
		return nil, fmt.Errorf("search failed: %s", resp.Error)
	}
	
	memories := make([]Memory, len(resp.Memories))
	for i, m := range resp.Memories {
		memories[i] = n.messageToMemory(m)
	}
	
	return memories, nil
}

// Forget marks a memory as forgotten
func (n *NATSStore) Forget(id string) error {
	req := messages.MemoryForgetRequest{ID: id}
	
	respData, err := n.client.Request("memory.forget", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryForgetResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("forget failed: %s", resp.Error)
	}
	
	return nil
}

// DecayImportance applies time-based decay to all memories
func (n *NATSStore) DecayImportance() error {
	req := messages.MemoryDecayRequest{}
	
	respData, err := n.client.Request("memory.decay", req, 30*time.Second) // Use longer timeout for decay
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryDecayResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("decay failed: %s", resp.Error)
	}
	
	return nil
}

// ListRecent returns the N most recently created memories
func (n *NATSStore) ListRecent(limit int, minImportance float64) ([]Memory, error) {
	req := messages.MemoryListRequest{Limit: limit, MinImportance: minImportance}
	
	respData, err := n.client.Request("memory.list", req, n.timeout)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryListResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	
	if resp.Error != "" {
		return nil, fmt.Errorf("list failed: %s", resp.Error)
	}
	
	memories := make([]Memory, len(resp.Memories))
	for i, m := range resp.Memories {
		memories[i] = n.messageToMemory(m)
	}
	
	return memories, nil
}

// Compact finds old, low-access memories and merges them
func (n *NATSStore) Compact(minAge time.Duration, minCount int) ([]CompactionResult, error) {
	req := messages.MemoryCompactRequest{MinAge: minAge, MinCount: minCount}
	
	respData, err := n.client.Request("memory.compact", req, 60*time.Second) // Use longer timeout for compaction
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryCompactResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	
	if resp.Error != "" {
		return nil, fmt.Errorf("compact failed: %s", resp.Error)
	}
	
	results := make([]CompactionResult, len(resp.Results))
	for i, r := range resp.Results {
		results[i] = CompactionResult{
			OriginalIDs: r.OriginalIDs,
			NewID:       r.NewID,
			Tags:        r.Tags,
			Count:       r.Count,
		}
	}
	
	return results, nil
}

// BuildContext retrieves memories suitable for including in a prompt
func (n *NATSStore) BuildContext(req BuildContextRequest) ([]Memory, int, error) {
	msgReq := messages.MemoryBuildContextRequest{
		Request: messages.BuildContextRequest{
			Tags:              req.Tags,
			TokenBudget:       req.TokenBudget,
			MinImportance:     req.MinImportance,
			ExcludeTags:       req.ExcludeTags,
			IncludeAlwaysLoad: req.IncludeAlwaysLoad,
			MaxChunkSize:      req.MaxChunkSize,
			TruncateThreshold: req.TruncateThreshold,
			TruncatePreview:   req.TruncatePreview,
		},
	}
	
	respData, err := n.client.Request("memory.build-context", msgReq, n.timeout)
	if err != nil {
		return nil, 0, fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryBuildContextResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, 0, fmt.Errorf("unmarshal response: %w", err)
	}
	
	if resp.Error != "" {
		return nil, 0, fmt.Errorf("build context failed: %s", resp.Error)
	}
	
	memories := make([]Memory, len(resp.Memories))
	for i, m := range resp.Memories {
		memories[i] = n.messageToMemory(m)
	}
	
	return memories, resp.TokenCount, nil
}

// PrepareSession loads identity and recent files into memory for a new session
func (n *NATSStore) PrepareSession(cfg PrepareSessionConfig) error {
	req := messages.MemoryPrepareSessionRequest{
		Config: messages.PrepareSessionConfig{
			RootDir:        cfg.RootDir,
			IdentityFile:   cfg.IdentityFile,
			IdentityText:   cfg.IdentityText,
			AgentName:      cfg.AgentName,
			RecencyWindow:  cfg.RecencyWindow,
			RecentFilesTTL: cfg.RecentFilesTTL,
		},
	}
	
	respData, err := n.client.Request("memory.prepare-session", req, 30*time.Second) // Longer timeout for session prep
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryPrepareSessionResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("prepare session failed: %s", resp.Error)
	}
	
	return nil
}

// LoadToolRegistry creates a memory entry describing available tools
func (n *NATSStore) LoadToolRegistry(tools []ToolMetadata) error {
	msgTools := make([]messages.ToolMetadata, len(tools))
	for i, t := range tools {
		msgTools[i] = messages.ToolMetadata{
			Name:        t.Name,
			Description: t.Description,
			Category:    t.Category,
		}
	}
	
	req := messages.MemoryLoadToolRegistryRequest{Tools: msgTools}
	
	respData, err := n.client.Request("memory.load-tool-registry", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryLoadToolRegistryResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("load tool registry failed: %s", resp.Error)
	}
	
	return nil
}

// UpdateToolUsageSummary saves a summary of a tool call result as ephemeral memory
func (n *NATSStore) UpdateToolUsageSummary(toolName, summary string, ttlSeconds int64) error {
	req := messages.MemoryUpdateToolUsageRequest{
		ToolName:   toolName,
		Summary:    summary,
		TTLSeconds: ttlSeconds,
	}
	
	respData, err := n.client.Request("memory.update-tool-usage", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryUpdateToolUsageResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("update tool usage failed: %s", resp.Error)
	}
	
	return nil
}

// SaveSession stores a session record
func (n *NATSStore) SaveSession(sess Session) error {
	req := messages.MemorySessionSaveRequest{
		Session: messages.Session{
			ID:           sess.ID,
			AgentName:    sess.AgentName,
			Model:        sess.Model,
			StartedAt:    sess.StartedAt,
			EndedAt:      sess.EndedAt,
			InputTokens:  sess.InputTokens,
			OutputTokens: sess.OutputTokens,
			Cost:         sess.Cost,
			Summary:      sess.Summary,
			Tags:         sess.Tags,
		},
	}
	
	respData, err := n.client.Request("memory.session-save", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemorySessionSaveResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("save session failed: %s", resp.Error)
	}
	
	return nil
}

// TrackMemoryUsage records when a memory was used in a session
func (n *NATSStore) TrackMemoryUsage(memoryID, sessionID string, turnNumber int, usageType string) error {
	req := messages.MemoryTrackUsageRequest{
		MemoryID:   memoryID,
		SessionID:  sessionID,
		TurnNumber: turnNumber,
		UsageType:  usageType,
	}
	
	respData, err := n.client.Request("memory.track-usage", req, n.timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}
	
	var resp messages.MemoryTrackUsageResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	
	if !resp.Success {
		return fmt.Errorf("track usage failed: %s", resp.Error)
	}
	
	return nil
}

// Close closes the NATS connection
func (n *NATSStore) Close() error {
	n.client.Close()
	return nil
}