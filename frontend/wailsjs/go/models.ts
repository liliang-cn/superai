export namespace agent {
	
	export class Deliverable {
	    path: string;
	    size: number;
	    type: string;
	
	    static createFrom(source: any = {}) {
	        return new Deliverable(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.type = source["type"];
	    }
	}
	export class PlanItem {
	    text: string;
	    done: boolean;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new PlanItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.done = source["done"];
	        this.note = source["note"];
	    }
	}
	export class ScheduledPrompt {
	    id: string;
	    prompt: string;
	    schedule: string;
	    note?: string;
	    session?: string;
	    enabled: boolean;
	    // Go type: time
	    next_run?: any;
	    // Go type: time
	    last_run?: any;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScheduledPrompt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.prompt = source["prompt"];
	        this.schedule = source["schedule"];
	        this.note = source["note"];
	        this.session = source["session"];
	        this.enabled = source["enabled"];
	        this.next_run = this.convertValues(source["next_run"], null);
	        this.last_run = this.convertValues(source["last_run"], null);
	        this.running = source["running"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace backend {
	
	export class CLIProxyAccount {
	    file: string;
	    provider: string;
	    account: string;
	    project: string;
	    expires: string;
	    disabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CLIProxyAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file = source["file"];
	        this.provider = source["provider"];
	        this.account = source["account"];
	        this.project = source["project"];
	        this.expires = source["expires"];
	        this.disabled = source["disabled"];
	    }
	}
	export class CLIProxyProvider {
	    id: string;
	    label: string;
	    needs_project: boolean;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new CLIProxyProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.needs_project = source["needs_project"];
	        this.note = source["note"];
	    }
	}
	export class ChatSessionInfo {
	    id: string;
	    title: string;
	    turns: number;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatSessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.turns = source["turns"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class ChatTurn {
	    role: string;
	    content: string;
	    emotion?: string;
	    kind?: string;
	    steps?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ChatTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.emotion = source["emotion"];
	        this.kind = source["kind"];
	        this.steps = source["steps"];
	    }
	}
	export class Dashboard {
	    id: string;
	    name: string;
	    source: string;
	    prompt: string;
	    cron?: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    refreshed_at: any;
	    last_error?: string;
	    refreshing?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Dashboard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.source = source["source"];
	        this.prompt = source["prompt"];
	        this.cron = source["cron"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.refreshed_at = this.convertValues(source["refreshed_at"], null);
	        this.last_error = source["last_error"];
	        this.refreshing = source["refreshing"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DoctorCheck {
	    name: string;
	    status: string;
	    detail: string;
	    fix?: string;
	
	    static createFrom(source: any = {}) {
	        return new DoctorCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.fix = source["fix"];
	    }
	}
	export class DoctorReport {
	    home: string;
	    healthy: boolean;
	    ok: number;
	    warn: number;
	    fail: number;
	    checks: DoctorCheck[];
	    error?: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new DoctorReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.home = source["home"];
	        this.healthy = source["healthy"];
	        this.ok = source["ok"];
	        this.warn = source["warn"];
	        this.fail = source["fail"];
	        this.checks = this.convertValues(source["checks"], DoctorCheck);
	        this.error = source["error"];
	        this.at = source["at"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LifeData {
	    schedules: any[];
	    records: any[];
	    persons: Record<string, any>;
	    reminders: any[];
	
	    static createFrom(source: any = {}) {
	        return new LifeData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schedules = source["schedules"];
	        this.records = source["records"];
	        this.persons = source["persons"];
	        this.reminders = source["reminders"];
	    }
	}
	export class LogLine {
	    at: string;
	    kind: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new LogLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.kind = source["kind"];
	        this.text = source["text"];
	    }
	}
	export class PreviewMessage {
	    role: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	    }
	}
	export class PromptPreview {
	    sessionId: string;
	    taskId: string;
	    model: string;
	    systemPrompt: string;
	    messages: PreviewMessage[];
	    tools: string[];
	    estimatedTokens: number;
	    constraintsDeclared: boolean;
	    constraintExtractionSkipped: boolean;
	    forbidTools: boolean;
	    deliverables: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new PromptPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.taskId = source["taskId"];
	        this.model = source["model"];
	        this.systemPrompt = source["systemPrompt"];
	        this.messages = this.convertValues(source["messages"], PreviewMessage);
	        this.tools = source["tools"];
	        this.estimatedTokens = source["estimatedTokens"];
	        this.constraintsDeclared = source["constraintsDeclared"];
	        this.constraintExtractionSkipped = source["constraintExtractionSkipped"];
	        this.forbidTools = source["forbidTools"];
	        this.deliverables = source["deliverables"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PulseBin {
	    sec: number;
	    tokens: number;
	    cached: number;
	    calls: number;
	    rounds: number;
	    reads: number;
	    writes: number;
	    shells: number;
	    fails: number;
	    mcp: number;
	    memory: number;
	    think: number;
	    cpu: number;
	    heap: number;
	
	    static createFrom(source: any = {}) {
	        return new PulseBin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sec = source["sec"];
	        this.tokens = source["tokens"];
	        this.cached = source["cached"];
	        this.calls = source["calls"];
	        this.rounds = source["rounds"];
	        this.reads = source["reads"];
	        this.writes = source["writes"];
	        this.shells = source["shells"];
	        this.fails = source["fails"];
	        this.mcp = source["mcp"];
	        this.memory = source["memory"];
	        this.think = source["think"];
	        this.cpu = source["cpu"];
	        this.heap = source["heap"];
	    }
	}
	export class PulseEvent {
	    seq: number;
	    at: string;
	    kind: string;
	    name: string;
	    text: string;
	    n?: number;
	    ms?: number;
	    bad?: boolean;
	    inner?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PulseEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.at = source["at"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.text = source["text"];
	        this.n = source["n"];
	        this.ms = source["ms"];
	        this.bad = source["bad"];
	        this.inner = source["inner"];
	    }
	}
	export class PulseRun {
	    runId: string;
	    model: string;
	    round: number;
	    session?: string;
	    doing: string;
	    since: string;
	    seen: string;
	    think?: string;
	
	    static createFrom(source: any = {}) {
	        return new PulseRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.model = source["model"];
	        this.round = source["round"];
	        this.session = source["session"];
	        this.doing = source["doing"];
	        this.since = source["since"];
	        this.seen = source["seen"];
	        this.think = source["think"];
	    }
	}
	export class PulseTool {
	    name: string;
	    calls: number;
	    errors: number;
	    lastAt: string;
	
	    static createFrom(source: any = {}) {
	        return new PulseTool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.calls = source["calls"];
	        this.errors = source["errors"];
	        this.lastAt = source["lastAt"];
	    }
	}
	export class PulseSnapshot {
	    now: string;
	    since: string;
	    live: boolean;
	    runs: PulseRun[];
	    bins: PulseBin[];
	    tools: PulseTool[];
	    events: PulseEvent[];
	    tokens: number;
	    cached: number;
	    rounds: number;
	    calls: number;
	    reads: number;
	    writes: number;
	    shells: number;
	    fails: number;
	    mcp: number;
	    memory: number;
	    errors: number;
	    peak: number;
	    peakThink: number;
	    heap: number;
	    heapLow: number;
	    heapHigh: number;
	    cpu: number;
	    goroutines: number;
	
	    static createFrom(source: any = {}) {
	        return new PulseSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.now = source["now"];
	        this.since = source["since"];
	        this.live = source["live"];
	        this.runs = this.convertValues(source["runs"], PulseRun);
	        this.bins = this.convertValues(source["bins"], PulseBin);
	        this.tools = this.convertValues(source["tools"], PulseTool);
	        this.events = this.convertValues(source["events"], PulseEvent);
	        this.tokens = source["tokens"];
	        this.cached = source["cached"];
	        this.rounds = source["rounds"];
	        this.calls = source["calls"];
	        this.reads = source["reads"];
	        this.writes = source["writes"];
	        this.shells = source["shells"];
	        this.fails = source["fails"];
	        this.mcp = source["mcp"];
	        this.memory = source["memory"];
	        this.errors = source["errors"];
	        this.peak = source["peak"];
	        this.peakThink = source["peakThink"];
	        this.heap = source["heap"];
	        this.heapLow = source["heapLow"];
	        this.heapHigh = source["heapHigh"];
	        this.cpu = source["cpu"];
	        this.goroutines = source["goroutines"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RoundStat {
	    segment: number;
	    round: number;
	    tokens: number;
	    cached: number;
	    tools: number;
	    text: number;
	    durMs: number;
	    compacted?: boolean;
	    lint?: string;
	    retried?: boolean;
	    failed?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RoundStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.segment = source["segment"];
	        this.round = source["round"];
	        this.tokens = source["tokens"];
	        this.cached = source["cached"];
	        this.tools = source["tools"];
	        this.text = source["text"];
	        this.durMs = source["durMs"];
	        this.compacted = source["compacted"];
	        this.lint = source["lint"];
	        this.retried = source["retried"];
	        this.failed = source["failed"];
	    }
	}
	export class SegmentStat {
	    index: number;
	    sessionId: string;
	    startedAt: string;
	    endedAt?: string;
	    stopReason?: string;
	    productive: boolean;
	    costUsd: number;
	    err?: string;
	    rounds: number;
	
	    static createFrom(source: any = {}) {
	        return new SegmentStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.sessionId = source["sessionId"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.stopReason = source["stopReason"];
	        this.productive = source["productive"];
	        this.costUsd = source["costUsd"];
	        this.err = source["err"];
	        this.rounds = source["rounds"];
	    }
	}
	export class Settings {
	    llm_base_url: string;
	    llm_key: string;
	    llm_model: string;
	    llm_price_input_per_1k?: number;
	    llm_price_cached_per_1k?: number;
	    llm_price_output_per_1k?: number;
	    embed_base_url: string;
	    embed_key: string;
	    embed_model: string;
	    search_base_url: string;
	    search_key: string;
	    search_model: string;
	    searxng_url: string;
	    disable_self_install: boolean;
	    disable_tool_approval: boolean;
	    workspace_dir: string;
	    max_rounds: number;
	    headless: boolean;
	    disable_browser: boolean;
	    disable_ptc: boolean;
	    pii_redaction: boolean;
	    avatar_port: number;
	    cliproxy_enabled: boolean;
	    cliproxy_port: number;
	    memory_backend: string;
	    shared_memory_endpoint: string;
	    shared_memory_token: string;
	    shared_memory_namespace: string;
	    webhook_url: string;
	    webhook_secret: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.llm_base_url = source["llm_base_url"];
	        this.llm_key = source["llm_key"];
	        this.llm_model = source["llm_model"];
	        this.llm_price_input_per_1k = source["llm_price_input_per_1k"];
	        this.llm_price_cached_per_1k = source["llm_price_cached_per_1k"];
	        this.llm_price_output_per_1k = source["llm_price_output_per_1k"];
	        this.embed_base_url = source["embed_base_url"];
	        this.embed_key = source["embed_key"];
	        this.embed_model = source["embed_model"];
	        this.search_base_url = source["search_base_url"];
	        this.search_key = source["search_key"];
	        this.search_model = source["search_model"];
	        this.searxng_url = source["searxng_url"];
	        this.disable_self_install = source["disable_self_install"];
	        this.disable_tool_approval = source["disable_tool_approval"];
	        this.workspace_dir = source["workspace_dir"];
	        this.max_rounds = source["max_rounds"];
	        this.headless = source["headless"];
	        this.disable_browser = source["disable_browser"];
	        this.disable_ptc = source["disable_ptc"];
	        this.pii_redaction = source["pii_redaction"];
	        this.avatar_port = source["avatar_port"];
	        this.cliproxy_enabled = source["cliproxy_enabled"];
	        this.cliproxy_port = source["cliproxy_port"];
	        this.memory_backend = source["memory_backend"];
	        this.shared_memory_endpoint = source["shared_memory_endpoint"];
	        this.shared_memory_token = source["shared_memory_token"];
	        this.shared_memory_namespace = source["shared_memory_namespace"];
	        this.webhook_url = source["webhook_url"];
	        this.webhook_secret = source["webhook_secret"];
	    }
	}
	export class SkillCandidate {
	    name: string;
	    description: string;
	    path: string;
	    installed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SkillCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.path = source["path"];
	        this.installed = source["installed"];
	    }
	}
	export class SkillInfo {
	    id: string;
	    name: string;
	    description: string;
	    when_to_use: string;
	    collection: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.when_to_use = source["when_to_use"];
	        this.collection = source["collection"];
	    }
	}
	export class TaskState {
	    taskId: string;
	    goal: string;
	    model: string;
	    startedAt: string;
	    endedAt?: string;
	    done: boolean;
	    running: boolean;
	    stop?: string;
	    final?: string;
	    maxSegments: number;
	    segments: SegmentStat[];
	    rounds: RoundStat[];
	    plan: agent.PlanItem[];
	    toolCounts: Record<string, number>;
	    toolCalls: number;
	    toolErrors: number;
	    lints: Record<string, number>;
	    lintRetries: number;
	    lintBlocks: number;
	    compactions: number;
	    retries: number;
	    checkpoints: number;
	    errors: number;
	    totalTokens: number;
	    totalCached: number;
	    costUsd: number;
	    unpriced?: boolean;
	    log: LogLine[];
	
	    static createFrom(source: any = {}) {
	        return new TaskState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.goal = source["goal"];
	        this.model = source["model"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.done = source["done"];
	        this.running = source["running"];
	        this.stop = source["stop"];
	        this.final = source["final"];
	        this.maxSegments = source["maxSegments"];
	        this.segments = this.convertValues(source["segments"], SegmentStat);
	        this.rounds = this.convertValues(source["rounds"], RoundStat);
	        this.plan = this.convertValues(source["plan"], agent.PlanItem);
	        this.toolCounts = source["toolCounts"];
	        this.toolCalls = source["toolCalls"];
	        this.toolErrors = source["toolErrors"];
	        this.lints = source["lints"];
	        this.lintRetries = source["lintRetries"];
	        this.lintBlocks = source["lintBlocks"];
	        this.compactions = source["compactions"];
	        this.retries = source["retries"];
	        this.checkpoints = source["checkpoints"];
	        this.errors = source["errors"];
	        this.totalTokens = source["totalTokens"];
	        this.totalCached = source["totalCached"];
	        this.costUsd = source["costUsd"];
	        this.unpriced = source["unpriced"];
	        this.log = this.convertValues(source["log"], LogLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TaskSummary {
	    taskId: string;
	    goal: string;
	    model: string;
	    startedAt: string;
	    endedAt?: string;
	    running: boolean;
	    done: boolean;
	    stop?: string;
	    segments: number;
	    maxSegments: number;
	    segmentOpen: boolean;
	    rounds: number;
	    lastTokens: number;
	    lastTools: number;
	    totalTokens: number;
	    totalCached: number;
	    costUsd: number;
	    unpriced?: boolean;
	    rejected: number;
	    errors: number;
	    planDone: number;
	    planTotal: number;
	    spark: number[];
	
	    static createFrom(source: any = {}) {
	        return new TaskSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.taskId = source["taskId"];
	        this.goal = source["goal"];
	        this.model = source["model"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.running = source["running"];
	        this.done = source["done"];
	        this.stop = source["stop"];
	        this.segments = source["segments"];
	        this.maxSegments = source["maxSegments"];
	        this.segmentOpen = source["segmentOpen"];
	        this.rounds = source["rounds"];
	        this.lastTokens = source["lastTokens"];
	        this.lastTools = source["lastTools"];
	        this.totalTokens = source["totalTokens"];
	        this.totalCached = source["totalCached"];
	        this.costUsd = source["costUsd"];
	        this.unpriced = source["unpriced"];
	        this.rejected = source["rejected"];
	        this.errors = source["errors"];
	        this.planDone = source["planDone"];
	        this.planTotal = source["planTotal"];
	        this.spark = source["spark"];
	    }
	}

}

export namespace mcp {
	
	export class ToolSummary {
	    name: string;
	    description: string;
	    server_name: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.server_name = source["server_name"];
	    }
	}
	export class ServerStatus {
	    name: string;
	    description: string;
	    command: string;
	    running: boolean;
	    tool_count: number;
	    tools?: ToolSummary[];
	
	    static createFrom(source: any = {}) {
	        return new ServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.command = source["command"];
	        this.running = source["running"];
	        this.tool_count = source["tool_count"];
	        this.tools = this.convertValues(source["tools"], ToolSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

