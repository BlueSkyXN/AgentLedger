package adapters

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"
)

const (
	traeWorkCNMainInspectorPort = 9229
	traeWorkCNAttachTimeout     = 10 * time.Second
	traeWorkCNCleanupTimeout    = 5 * time.Second
)

type traeWorkCNRuntime interface {
	Collect(ctx context.Context, paths []string) ([]string, []string, error)
	Probe(paths []string) (DirectSourceProbe, error)
}

type traeWorkCNRuntimeCollector struct {
	platformSupported func() bool
	findProcesses     func(paths []string) ([]int, error)
	collectProcess    func(ctx context.Context, pid int) (string, []string, error)
}

func newTraeWorkCNRuntimeCollector() *traeWorkCNRuntimeCollector {
	return &traeWorkCNRuntimeCollector{
		platformSupported: traeWorkCNPlatformSupported,
		findProcesses:     findTraeWorkCNProcesses,
		collectProcess:    collectTraeWorkCNProcess,
	}
}

func (c *traeWorkCNRuntimeCollector) Probe(paths []string) (DirectSourceProbe, error) {
	if c == nil || c.platformSupported == nil || !c.platformSupported() {
		return DirectSourceProbe{Supported: false}, nil
	}
	processes, err := c.findProcesses(paths)
	if err != nil {
		return DirectSourceProbe{Supported: true}, err
	}
	return DirectSourceProbe{Supported: true, RunningSources: len(processes)}, nil
}

func (c *traeWorkCNRuntimeCollector) Collect(ctx context.Context, paths []string) ([]string, []string, error) {
	if c == nil || c.platformSupported == nil || !c.platformSupported() {
		return nil, nil, fmt.Errorf("TRAE Work CN direct scan is unsupported on this platform")
	}
	processes, err := c.findProcesses(paths)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover a running TRAE Work CN process")
	}
	if len(processes) == 0 {
		return nil, nil, fmt.Errorf("TRAE Work CN is not running")
	}
	sort.Ints(processes)

	payloads := make([]string, 0, len(processes))
	warnings := make([]string, 0)
	for _, pid := range processes {
		payload, processWarnings, collectErr := c.collectProcess(ctx, pid)
		warnings = append(warnings, processWarnings...)
		if collectErr != nil {
			warnings = append(warnings, fmt.Sprintf("one running TRAE Work CN process could not be scanned: %v", collectErr))
			continue
		}
		payloads = append(payloads, payload)
	}
	if len(payloads) == 0 {
		return nil, warnings, fmt.Errorf("TRAE Work CN runtime scan failed")
	}
	return payloads, warnings, nil
}

func collectTraeWorkCNProcess(ctx context.Context, pid int) (payload string, warnings []string, err error) {
	session, err := openTraeWorkCNDebugSession(ctx, pid)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		warnings = append(warnings, session.Close()...)
	}()

	if err := session.renderer.Evaluate(ctx, traeWorkCNRuntimeExpression, false, &payload); err != nil {
		return "", warnings, fmt.Errorf("TRAE Work CN renderer usage projection failed")
	}
	if payload == "" {
		return "", warnings, fmt.Errorf("TRAE Work CN renderer returned an empty usage projection")
	}
	return payload, warnings, nil
}

type traeWorkCNDebugSession struct {
	pid             int
	mainPort        int
	main            *cdpClient
	renderer        *cdpClient
	rendererPort    int
	mainOpenedByUs  bool
	remoteDebugging bool
}

func openTraeWorkCNDebugSession(ctx context.Context, pid int) (_ *traeWorkCNDebugSession, returnErr error) {
	attachCtx, cancel := context.WithTimeout(ctx, traeWorkCNAttachTimeout)
	defer cancel()

	session := &traeWorkCNDebugSession{pid: pid, mainPort: traeWorkCNMainInspectorPort}
	defer func() {
		if returnErr != nil {
			if warnings := session.Close(); len(warnings) > 0 {
				returnErr = fmt.Errorf("%w; debugger cleanup could not be confirmed", returnErr)
			}
		}
	}()

	mainTargets, listening := currentDebugTargets(attachCtx, traeWorkCNMainInspectorPort)
	if !listening {
		if err := signalTraeWorkCNInspector(pid); err != nil {
			return nil, fmt.Errorf("failed to open the TRAE Work CN main inspector")
		}
		session.mainOpenedByUs = true
		var err error
		mainTargets, err = waitForDebugTargets(attachCtx, traeWorkCNMainInspectorPort)
		if err != nil {
			return nil, fmt.Errorf("TRAE Work CN main inspector did not become ready")
		}
	} else if len(mainTargets) == 0 {
		return nil, fmt.Errorf("Node inspector port is occupied by an incompatible service")
	}

	main, err := dialFirstCDPTarget(attachCtx, mainTargets, traeWorkCNMainInspectorPort)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the TRAE Work CN main inspector")
	}
	var inspectedPID int
	if err := main.Evaluate(attachCtx, "process.pid", false, &inspectedPID); err != nil || inspectedPID != pid {
		main.Close()
		return nil, fmt.Errorf("Node inspector port belongs to another process")
	}
	// Only a PID-verified client may enter the owned cleanup path.
	session.main = main

	rendererPort, err := freeLoopbackPort()
	if err != nil {
		return nil, fmt.Errorf("failed to reserve a local renderer debug port")
	}
	session.rendererPort = rendererPort
	startExpression := fmt.Sprintf(`(async () => {
		const { ahaDebugger } = require("electron");
		return Boolean(await ahaDebugger.startRemoteDebugging(%d));
	})()`, rendererPort)
	session.remoteDebugging = true
	var started bool
	if err := main.Evaluate(attachCtx, startExpression, true, &started); err != nil || !started {
		return nil, fmt.Errorf("TRAE Work CN renderer debugging could not be started")
	}

	rendererTargets, err := waitForDebugTargets(attachCtx, rendererPort)
	if err != nil {
		return nil, fmt.Errorf("TRAE Work CN renderer debug target did not become ready")
	}
	for _, target := range rendererTargets {
		candidate, dialErr := dialCDPTarget(attachCtx, target, rendererPort)
		if dialErr != nil {
			continue
		}
		var ready bool
		readyErr := candidate.Evaluate(attachCtx, "Boolean(globalThis.icubeStore?.serviceCollection?.getEntries)", false, &ready)
		if readyErr == nil && ready {
			session.renderer = candidate
			break
		}
		candidate.Close()
	}
	if session.renderer == nil {
		return nil, fmt.Errorf("no compatible TRAE Work CN renderer was found")
	}
	return session, nil
}

func (s *traeWorkCNDebugSession) Close() []string {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), traeWorkCNCleanupTimeout)
	defer cancel()
	warnings := make([]string, 0)
	if s.main == nil && s.mainOpenedByUs {
		s.recoverOwnedMain(ctx)
	}

	if s.remoteDebugging && s.main != nil {
		var stopped bool
		stopExpression := `(async () => {
			const { ahaDebugger } = require("electron");
			return Boolean(await ahaDebugger.stopRemoteDebugging());
		})()`
		if err := s.main.Evaluate(ctx, stopExpression, true, &stopped); err != nil || !stopped {
			warnings = append(warnings, "renderer debugging cleanup could not be confirmed")
		}
	}
	if s.renderer != nil {
		s.renderer.Close()
		s.renderer = nil
	}
	if s.rendererPort > 0 && !waitForLoopbackPortClosed(ctx, s.rendererPort) {
		warnings = append(warnings, "renderer debug port remained open after cleanup")
	}

	if s.main != nil && s.mainOpenedByUs {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		_ = s.main.Evaluate(closeCtx, `require("inspector").close(); true`, true, new(bool))
		closeCancel()
	}
	if s.main != nil {
		s.main.Close()
		s.main = nil
	}
	if s.mainOpenedByUs {
		firstWaitCtx, firstWaitCancel := context.WithTimeout(context.Background(), time.Second)
		closed := waitForLoopbackPortClosed(firstWaitCtx, s.mainInspectorPort())
		firstWaitCancel()
		if !closed {
			recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 2*time.Second)
			s.recoverOwnedMain(recoveryCtx)
			if s.main != nil {
				_ = s.main.Evaluate(recoveryCtx, `require("inspector").close(); true`, true, new(bool))
				s.main.Close()
				s.main = nil
			}
			recoveryCancel()
			finalWaitCtx, finalWaitCancel := context.WithTimeout(context.Background(), 2*time.Second)
			closed = waitForLoopbackPortClosed(finalWaitCtx, s.mainInspectorPort())
			finalWaitCancel()
		}
		if !closed {
			warnings = append(warnings, "main inspector port remained open after cleanup")
		}
	}
	return warnings
}

func (s *traeWorkCNDebugSession) recoverOwnedMain(ctx context.Context) {
	if s == nil || !s.mainOpenedByUs || s.main != nil || !loopbackPortOpen(s.mainInspectorPort()) {
		return
	}
	targets, err := fetchCDPTargets(ctx, s.mainInspectorPort())
	if err != nil {
		return
	}
	main, err := dialFirstCDPTarget(ctx, targets, s.mainInspectorPort())
	if err != nil {
		return
	}
	var inspectedPID int
	if err := main.Evaluate(ctx, "process.pid", false, &inspectedPID); err != nil || inspectedPID != s.pid {
		main.Close()
		return
	}
	s.main = main
}

func (s *traeWorkCNDebugSession) mainInspectorPort() int {
	if s != nil && s.mainPort > 0 {
		return s.mainPort
	}
	return traeWorkCNMainInspectorPort
}

func currentDebugTargets(ctx context.Context, port int) ([]cdpTarget, bool) {
	if !loopbackPortOpen(port) {
		return nil, false
	}
	targets, err := fetchCDPTargets(ctx, port)
	if err != nil {
		return nil, true
	}
	return targets, true
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func loopbackPortOpen(port int) bool {
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func waitForLoopbackPortClosed(ctx context.Context, port int) bool {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !loopbackPortOpen(port) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

const traeWorkCNRuntimeExpression = `(async () => {
	const MAX_SESSIONS = 10000;
	const MAX_MESSAGES = 250000;
	const PAGE_SIZE = 200;
	const serviceCollection = globalThis.icubeStore?.serviceCollection;
	if (!serviceCollection?.getEntries) throw new Error("TRAE runtime service collection unavailable");
	const connectionEntry = Array.from(serviceCollection.getEntries())
		.find(([key]) => String(key) === "IICubeAiChatConnectionService");
	if (!connectionEntry?.[1]?.getDefaultClient) throw new Error("TRAE runtime connection service unavailable");
	const client = await connectionEntry[1].getDefaultClient();

	const request = async (method, data) => {
		const response = await client.request({
			service: "lite",
			method,
			data,
			context: {},
			timeout: 10000,
		});
		if (!response || response.code !== 0) throw new Error("TRAE runtime request failed");
		const inner = response.data;
		if (!inner || inner.code !== 0 || !inner.data || typeof inner.data !== "object") {
			throw new Error("TRAE runtime response contract mismatch");
		}
		return inner.data;
	};

	const sessions = [];
	const seenSessionIDs = new Set();
	let offset = 0;
	for (let page = 0; page < 1000; page++) {
		const payload = await request("list_chat_sessions", { limit: PAGE_SIZE, offset });
		const items = Array.isArray(payload.items) ? payload.items : [];
		let added = 0;
		for (const item of items) {
			const sessionID = typeof item?.chat_session_id === "string" ? item.chat_session_id.trim() : "";
			if (!sessionID || sessionID.length > 512 || seenSessionIDs.has(sessionID)) continue;
			seenSessionIDs.add(sessionID);
			sessions.push(item);
			added++;
			if (sessions.length > MAX_SESSIONS) throw new Error("TRAE runtime session limit exceeded");
		}
		const total = Number(payload.total);
		const hasTotal = Number.isSafeInteger(total) && total >= 0;
		if ((hasTotal && sessions.length >= total) || items.length === 0 || added === 0 || (!hasTotal && items.length < PAGE_SIZE)) break;
		offset += items.length;
	}

	const result = {
		schema: "agentledger.trae-work-cn.runtime.v1",
		sessions_scanned: sessions.length,
		messages_scanned: 0,
		assistant_messages: 0,
		duplicate_messages: 0,
		skipped_missing_identity: 0,
		skipped_invalid_timestamp: 0,
		skipped_missing_usage: 0,
		skipped_invalid_usage: 0,
		records: [],
	};
	const tokenFields = [
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
		"reasoning_tokens",
		"prompt_tokens_total",
		"completion_tokens_total",
		"last_turn_total_tokens",
		"max_tokens",
	];
	const toTimestampMs = (value) => {
		const numeric = Number(value);
		if (!Number.isFinite(numeric) || numeric <= 0) return null;
		const timestamp = numeric < 1e12 ? Math.round(numeric * 1000) : Math.round(numeric);
		return Number.isSafeInteger(timestamp) && timestamp > 0 ? timestamp : null;
	};
	const safeMode = (value) => ["work", "code", "design"].includes(value) ? value : "";
	const safeAgentType = (value) => {
		if (typeof value !== "string") return "";
		const normalized = value.trim();
		return normalized.length <= 64 && /^solo_[a-z0-9_-]+$/.test(normalized) ? normalized : "";
	};
	const safeModel = (value) => {
		if (typeof value !== "string") return "unknown";
		const normalized = value.trim();
		return /^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,127}$/.test(normalized) ? normalized : "unknown";
	};
	const parseBoundedObject = (value, maxLength = 65536) => {
		if (typeof value === "string") {
			if (value.length > maxLength) return null;
			try { value = JSON.parse(value); } catch { return null; }
		}
		return value && typeof value === "object" && !Array.isArray(value) ? value : null;
	};

	for (const session of sessions) {
		const sessionID = session.chat_session_id.trim();
		const seenMessageIDs = new Set();
		const seenPageTokens = new Set();
		let nextPageToken;
		for (let page = 0; page < 1000; page++) {
			const data = { chat_session_id: sessionID, page_size: PAGE_SIZE };
			if (nextPageToken !== undefined && nextPageToken !== null && String(nextPageToken) !== "") {
				data.next_page_token = nextPageToken;
			}
			const payload = await request("get_messages", data);
			const messages = Array.isArray(payload.items) ? payload.items : [];
			let added = 0;
			for (const message of messages) {
				const messageID = typeof message?.message_id === "string" ? message.message_id.trim() : "";
				if (messageID && seenMessageIDs.has(messageID)) {
					result.duplicate_messages++;
					continue;
				}
				if (messageID) seenMessageIDs.add(messageID);
				added++;
				result.messages_scanned++;
				if (result.messages_scanned > MAX_MESSAGES) throw new Error("TRAE runtime message limit exceeded");
				if (message?.role !== "assistant") continue;
				result.assistant_messages++;
				const nativeSessionID = typeof message?.chat_session_id === "string" ? message.chat_session_id.trim() : sessionID;
				if (!nativeSessionID || nativeSessionID.length > 512 || !messageID || messageID.length > 512) {
					result.skipped_missing_identity++;
					continue;
				}
				const timestampMs = toTimestampMs(message.created_at);
				if (!timestampMs) {
					result.skipped_invalid_timestamp++;
					continue;
				}
				const rawUsage = message.token_usage;
				const usage = parseBoundedObject(rawUsage);
				if (!usage) {
					if (rawUsage === undefined || rawUsage === null || rawUsage === "") result.skipped_missing_usage++;
					else result.skipped_invalid_usage++;
					continue;
				}
				const tokenUsage = {};
				let invalidUsage = false;
				for (const field of tokenFields) {
					if (usage[field] === undefined || usage[field] === null) continue;
					const value = usage[field];
					if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
						invalidUsage = true;
						break;
					}
					tokenUsage[field] = value;
				}
				const total = tokenUsage.total_tokens;
				const prompt = tokenUsage.prompt_tokens ?? 0;
				const completion = tokenUsage.completion_tokens ?? 0;
				if (invalidUsage || !Number.isSafeInteger(total) || total <= 0 || prompt > total || completion > total - prompt) {
					result.skipped_invalid_usage++;
					continue;
				}
				const modelMeta = parseBoundedObject(message.model_smart_selection_meta);
				result.records.push({
					schema: "agentledger.trae-work-cn.usage.v1",
					session_id: nativeSessionID,
					message_id: messageID,
					timestamp_ms: timestampMs,
					model: safeModel(modelMeta?.config_name),
					mode: safeMode(session.mode),
					agent_type: safeAgentType(message.agent_type),
					token_usage: tokenUsage,
				});
			}

			const totalMessages = Number(payload.total_messages_count);
			if (Number.isSafeInteger(totalMessages) && totalMessages >= 0 && seenMessageIDs.size >= totalMessages) break;
			const candidate = payload.next_page_token;
			if (candidate === undefined || candidate === null || String(candidate) === "" || messages.length === 0 || added === 0) break;
			const candidateKey = String(candidate);
			if (seenPageTokens.has(candidateKey)) break;
			seenPageTokens.add(candidateKey);
			nextPageToken = candidate;
		}
	}

	result.records.sort((left, right) =>
		left.timestamp_ms - right.timestamp_ms ||
		left.session_id.localeCompare(right.session_id) ||
		left.message_id.localeCompare(right.message_id));
	return JSON.stringify(result);
})()`
