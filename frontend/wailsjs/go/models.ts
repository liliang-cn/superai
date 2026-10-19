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
	    id?: string;
	    after?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PlanItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.done = source["done"];
	        this.note = source["note"];
	        this.id = source["id"];
	        this.after = source["after"];
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
	export class StandingEvent {
	    source: string;
	    kind?: string;
	    payload: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new StandingEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = source["source"];
	        this.kind = source["kind"];
	        this.payload = source["payload"];
	        this.at = this.convertValues(source["at"], null);
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
	export class Wake {
	    id: string;
	    responsibility_id: string;
	    kind: string;
	    reason?: string;
	    event?: StandingEvent;
	    session_id: string;
	    run_id: string;
	    // Go type: time
	    started_at: any;
	    // Go type: time
	    ended_at?: any;
	    usage?: domain.TokenUsage;
	    tool_calls: number;
	    error?: string;
	    notified: number;
	
	    static createFrom(source: any = {}) {
	        return new Wake(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.responsibility_id = source["responsibility_id"];
	        this.kind = source["kind"];
	        this.reason = source["reason"];
	        this.event = this.convertValues(source["event"], StandingEvent);
	        this.session_id = source["session_id"];
	        this.run_id = source["run_id"];
	        this.started_at = this.convertValues(source["started_at"], null);
	        this.ended_at = this.convertValues(source["ended_at"], null);
	        this.usage = this.convertValues(source["usage"], domain.TokenUsage);
	        this.tool_calls = source["tool_calls"];
	        this.error = source["error"];
	        this.notified = source["notified"];
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

export namespace app {
	
	export class ActionOptions {
	    confirmed: boolean;
	    sessionId: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ActionOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.confirmed = source["confirmed"];
	        this.sessionId = source["sessionId"];
	        this.label = source["label"];
	    }
	}
	export class AgentLinkOptions {
	    Core: string;
	    Token: string;
	    Name: string;
	    TLS: boolean;
	    Version: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentLinkOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Core = source["Core"];
	        this.Token = source["Token"];
	        this.Name = source["Name"];
	        this.TLS = source["TLS"];
	        this.Version = source["Version"];
	    }
	}
	export class AgentReport {
	    push: boolean;
	    telegram: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AgentReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.push = source["push"];
	        this.telegram = source["telegram"];
	    }
	}
	export class AgentReportEntry {
	    agent: string;
	    name: string;
	    kind: string;
	    message: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new AgentReportEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.message = source["message"];
	        this.at = this.convertValues(source["at"], null);
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
	export class AgentSpec {
	    id: string;
	    name: string;
	    glyph?: string;
	    hue?: string;
	    goal: string;
	    watch?: string[];
	    attention?: string;
	    never?: string[];
	    auto?: string[];
	    ask?: string[];
	    forbid?: string[];
	    allConnectors: boolean;
	    connectors?: string[];
	    cron?: string;
	    everyMinutes?: number;
	    scanEveryMinutes?: number;
	    report: AgentReport;
	    maxWakesPerDay?: number;
	    hookSecret?: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new AgentSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.glyph = source["glyph"];
	        this.hue = source["hue"];
	        this.goal = source["goal"];
	        this.watch = source["watch"];
	        this.attention = source["attention"];
	        this.never = source["never"];
	        this.auto = source["auto"];
	        this.ask = source["ask"];
	        this.forbid = source["forbid"];
	        this.allConnectors = source["allConnectors"];
	        this.connectors = source["connectors"];
	        this.cron = source["cron"];
	        this.everyMinutes = source["everyMinutes"];
	        this.scanEveryMinutes = source["scanEveryMinutes"];
	        this.report = this.convertValues(source["report"], AgentReport);
	        this.maxWakesPerDay = source["maxWakesPerDay"];
	        this.hookSecret = source["hookSecret"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
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
	export class AgentView {
	    id: string;
	    name: string;
	    glyph?: string;
	    hue?: string;
	    goal: string;
	    watch?: string[];
	    attention?: string;
	    never?: string[];
	    auto?: string[];
	    ask?: string[];
	    forbid?: string[];
	    allConnectors: boolean;
	    connectors?: string[];
	    cron?: string;
	    everyMinutes?: number;
	    scanEveryMinutes?: number;
	    report: AgentReport;
	    maxWakesPerDay?: number;
	    hookSecret?: string;
	    // Go type: time
	    createdAt: any;
	    paused: boolean;
	    pausedReason?: string;
	    notes?: string;
	    running?: agent.Wake;
	    lastWake?: agent.Wake;
	    // Go type: time
	    nextDue?: any;
	    nextDueKind?: string;
	    wakesToday: number;
	    hookPath?: string;
	    waitingFor?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.glyph = source["glyph"];
	        this.hue = source["hue"];
	        this.goal = source["goal"];
	        this.watch = source["watch"];
	        this.attention = source["attention"];
	        this.never = source["never"];
	        this.auto = source["auto"];
	        this.ask = source["ask"];
	        this.forbid = source["forbid"];
	        this.allConnectors = source["allConnectors"];
	        this.connectors = source["connectors"];
	        this.cron = source["cron"];
	        this.everyMinutes = source["everyMinutes"];
	        this.scanEveryMinutes = source["scanEveryMinutes"];
	        this.report = this.convertValues(source["report"], AgentReport);
	        this.maxWakesPerDay = source["maxWakesPerDay"];
	        this.hookSecret = source["hookSecret"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.paused = source["paused"];
	        this.pausedReason = source["pausedReason"];
	        this.notes = source["notes"];
	        this.running = this.convertValues(source["running"], agent.Wake);
	        this.lastWake = this.convertValues(source["lastWake"], agent.Wake);
	        this.nextDue = this.convertValues(source["nextDue"], null);
	        this.nextDueKind = source["nextDueKind"];
	        this.wakesToday = source["wakesToday"];
	        this.hookPath = source["hookPath"];
	        this.waitingFor = source["waitingFor"];
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
	export class AttentionItem {
	    level: string;
	    kind: string;
	    title: string;
	    detail?: string;
	    // Go type: time
	    at?: any;
	    zone?: string;
	    place?: string;
	    ref?: string;
	    open?: string;
	
	    static createFrom(source: any = {}) {
	        return new AttentionItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.level = source["level"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.at = this.convertValues(source["at"], null);
	        this.zone = source["zone"];
	        this.place = source["place"];
	        this.ref = source["ref"];
	        this.open = source["open"];
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
	export class CCActivitySession {
	    id: string;
	    project?: string;
	    state: string;
	    wait?: string;
	    tool?: string;
	    tools: number;
	    // Go type: time
	    since: any;
	    // Go type: time
	    started: any;
	    // Go type: time
	    last: any;
	
	    static createFrom(source: any = {}) {
	        return new CCActivitySession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.project = source["project"];
	        this.state = source["state"];
	        this.wait = source["wait"];
	        this.tool = source["tool"];
	        this.tools = source["tools"];
	        this.since = this.convertValues(source["since"], null);
	        this.started = this.convertValues(source["started"], null);
	        this.last = this.convertValues(source["last"], null);
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
	export class CCActivity {
	    working: number;
	    waiting: number;
	    idle: number;
	    sessions: CCActivitySession[];
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new CCActivity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.working = source["working"];
	        this.waiting = source["waiting"];
	        this.idle = source["idle"];
	        this.sessions = this.convertValues(source["sessions"], CCActivitySession);
	        this.at = this.convertValues(source["at"], null);
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
	
	export class CCDelivery {
	    via: string;
	    immediate: boolean;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new CCDelivery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.via = source["via"];
	        this.immediate = source["immediate"];
	        this.note = source["note"];
	    }
	}
	export class CCPresence {
	    away: boolean;
	    at?: string;
	
	    static createFrom(source: any = {}) {
	        return new CCPresence(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.away = source["away"];
	        this.at = source["at"];
	    }
	}
	export class CCReplyRequest {
	    agent: string;
	    session: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new CCReplyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.session = source["session"];
	        this.text = source["text"];
	    }
	}
	export class CCSession {
	    agent: string;
	    session: string;
	    host?: string;
	    project?: string;
	    cwd?: string;
	    state: string;
	    text?: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new CCSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.session = source["session"];
	        this.host = source["host"];
	        this.project = source["project"];
	        this.cwd = source["cwd"];
	        this.state = source["state"];
	        this.text = source["text"];
	        this.at = this.convertValues(source["at"], null);
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
	export class CLIActivity {
	    working: number;
	    waiting: number;
	    idle: number;
	    sessions: CCActivitySession[];
	    // Go type: time
	    at: any;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new CLIActivity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.working = source["working"];
	        this.waiting = source["waiting"];
	        this.idle = source["idle"];
	        this.sessions = this.convertValues(source["sessions"], CCActivitySession);
	        this.at = this.convertValues(source["at"], null);
	        this.source = source["source"];
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
	export class CLIRunEvent {
	    seq: number;
	    // Go type: time
	    at: any;
	    kind: string;
	    text?: string;
	    tool?: string;
	    detail?: string;
	    callId?: string;
	    failed?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CLIRunEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.at = this.convertValues(source["at"], null);
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.tool = source["tool"];
	        this.detail = source["detail"];
	        this.callId = source["callId"];
	        this.failed = source["failed"];
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
	export class CLIRun {
	    id: string;
	    thread: string;
	    agent: string;
	    prompt: string;
	    cwd: string;
	    model?: string;
	    session?: string;
	    ask: boolean;
	    chat?: string;
	    queen?: boolean;
	    remote?: string;
	    remoteRun?: string;
	    state: string;
	    // Go type: time
	    started: any;
	    // Go type: time
	    ended?: any;
	    summary?: string;
	    error?: string;
	    in: number;
	    out: number;
	    cache: number;
	    costUsd: number;
	    tools: number;
	    events?: CLIRunEvent[];
	
	    static createFrom(source: any = {}) {
	        return new CLIRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.thread = source["thread"];
	        this.agent = source["agent"];
	        this.prompt = source["prompt"];
	        this.cwd = source["cwd"];
	        this.model = source["model"];
	        this.session = source["session"];
	        this.ask = source["ask"];
	        this.chat = source["chat"];
	        this.queen = source["queen"];
	        this.remote = source["remote"];
	        this.remoteRun = source["remoteRun"];
	        this.state = source["state"];
	        this.started = this.convertValues(source["started"], null);
	        this.ended = this.convertValues(source["ended"], null);
	        this.summary = source["summary"];
	        this.error = source["error"];
	        this.in = source["in"];
	        this.out = source["out"];
	        this.cache = source["cache"];
	        this.costUsd = source["costUsd"];
	        this.tools = source["tools"];
	        this.events = this.convertValues(source["events"], CLIRunEvent);
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
	
	export class DetectedAgent {
	    name: string;
	    label: string;
	    path: string;
	    signedIn?: boolean;
	    canBeDefault: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DetectedAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.label = source["label"];
	        this.path = source["path"];
	        this.signedIn = source["signedIn"];
	        this.canBeDefault = source["canBeDefault"];
	    }
	}
	export class HiveLinkEntry {
	    id: string;
	    name: string;
	    url: string;
	    active: boolean;
	    // Go type: time
	    linkedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new HiveLinkEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	        this.active = source["active"];
	        this.linkedAt = this.convertValues(source["linkedAt"], null);
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
	export class HiveLinkInfo {
	    linked: boolean;
	    url?: string;
	    name?: string;
	    live: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new HiveLinkInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.linked = source["linked"];
	        this.url = source["url"];
	        this.name = source["name"];
	        this.live = source["live"];
	        this.error = source["error"];
	    }
	}
	export class MemberPulse {
	    name: string;
	    live: boolean;
	    tokens: number;
	    cached: number;
	    rounds: number;
	    calls: number;
	    fails: number;
	    mcp: number;
	    cpu: number;
	    heap: number;
	    tokPerSec: number;
	    doing?: string;
	    round?: number;
	    at: string;
	    ccWorking?: number;
	    ccWaiting?: number;
	
	    static createFrom(source: any = {}) {
	        return new MemberPulse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.live = source["live"];
	        this.tokens = source["tokens"];
	        this.cached = source["cached"];
	        this.rounds = source["rounds"];
	        this.calls = source["calls"];
	        this.fails = source["fails"];
	        this.mcp = source["mcp"];
	        this.cpu = source["cpu"];
	        this.heap = source["heap"];
	        this.tokPerSec = source["tokPerSec"];
	        this.doing = source["doing"];
	        this.round = source["round"];
	        this.at = source["at"];
	        this.ccWorking = source["ccWorking"];
	        this.ccWaiting = source["ccWaiting"];
	    }
	}
	export class HivePulse {
	    members: MemberPulse[];
	    total: MemberPulse;
	
	    static createFrom(source: any = {}) {
	        return new HivePulse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.members = this.convertValues(source["members"], MemberPulse);
	        this.total = this.convertValues(source["total"], MemberPulse);
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
	export class HostAgent {
	    name: string;
	    kind: string;
	    role?: string;
	    about?: string;
	    state: string;
	    activity?: string;
	    version?: string;
	    via?: string;
	    mcp?: string[];
	    service?: string;
	
	    static createFrom(source: any = {}) {
	        return new HostAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.role = source["role"];
	        this.about = source["about"];
	        this.state = source["state"];
	        this.activity = source["activity"];
	        this.version = source["version"];
	        this.via = source["via"];
	        this.mcp = source["mcp"];
	        this.service = source["service"];
	    }
	}
	export class Host {
	    id: string;
	    name: string;
	    hostname?: string;
	    kind: string;
	    os?: string;
	    arch?: string;
	    parent?: string;
	    state: string;
	    // Go type: time
	    lastSeen?: any;
	    addresses?: string[];
	    agents: HostAgent[];
	
	    static createFrom(source: any = {}) {
	        return new Host(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.hostname = source["hostname"];
	        this.kind = source["kind"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.parent = source["parent"];
	        this.state = source["state"];
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	        this.addresses = source["addresses"];
	        this.agents = this.convertValues(source["agents"], HostAgent);
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
	export class HiveTopology {
	    hosts: Host[];
	    services: backend.HAService[];
	
	    static createFrom(source: any = {}) {
	        return new HiveTopology(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hosts = this.convertValues(source["hosts"], Host);
	        this.services = this.convertValues(source["services"], backend.HAService);
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
	export class HiveWorker {
	    name: string;
	    kind: string;
	    host: string;
	    state: string;
	    up: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HiveWorker(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.host = source["host"];
	        this.state = source["state"];
	        this.up = source["up"];
	    }
	}
	
	
	export class HostDisk {
	    mount: string;
	    total: number;
	    used: number;
	
	    static createFrom(source: any = {}) {
	        return new HostDisk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mount = source["mount"];
	        this.total = source["total"];
	        this.used = source["used"];
	    }
	}
	export class HostExecResult {
	    exit: number;
	    output: string;
	    truncated?: boolean;
	    ms: number;
	
	    static createFrom(source: any = {}) {
	        return new HostExecResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exit = source["exit"];
	        this.output = source["output"];
	        this.truncated = source["truncated"];
	        this.ms = source["ms"];
	    }
	}
	export class HostMemory {
	    total: number;
	    used: number;
	
	    static createFrom(source: any = {}) {
	        return new HostMemory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.used = source["used"];
	    }
	}
	export class HostInfo {
	    name: string;
	    host: string;
	    os: string;
	    arch: string;
	    kernel?: string;
	    uptimeSeconds: number;
	    cpus: number;
	    load: number[];
	    memory: HostMemory;
	    disks: HostDisk[];
	    version?: string;
	    control: boolean;
	    claude?: CCActivity;
	    cliActivity?: Record<string, CLIActivity>;
	
	    static createFrom(source: any = {}) {
	        return new HostInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.kernel = source["kernel"];
	        this.uptimeSeconds = source["uptimeSeconds"];
	        this.cpus = source["cpus"];
	        this.load = source["load"];
	        this.memory = this.convertValues(source["memory"], HostMemory);
	        this.disks = this.convertValues(source["disks"], HostDisk);
	        this.version = source["version"];
	        this.control = source["control"];
	        this.claude = this.convertValues(source["claude"], CCActivity);
	        this.cliActivity = this.convertValues(source["cliActivity"], CLIActivity, true);
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
	
	export class HostService {
	    name: string;
	    state: string;
	    about?: string;
	    pid?: number;
	    lastExit?: number;
	
	    static createFrom(source: any = {}) {
	        return new HostService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.state = source["state"];
	        this.about = source["about"];
	        this.pid = source["pid"];
	        this.lastExit = source["lastExit"];
	    }
	}
	export class IntegrationToken {
	    id: string;
	    name: string;
	    scope: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    last_used: any;
	
	    static createFrom(source: any = {}) {
	        return new IntegrationToken(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.last_used = this.convertValues(source["last_used"], null);
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
	export class LinkedAgent {
	    name: string;
	    host: string;
	    os: string;
	    arch: string;
	    kind?: string;
	    parent?: string;
	    version: string;
	    clis: string[];
	    agents: any[];
	    // Go type: time
	    connected: any;
	    // Go type: time
	    lastSeen: any;
	    machine: boolean;
	    role: string;
	    activity?: CCActivity;
	    cliActivity?: Record<string, CLIActivity>;
	
	    static createFrom(source: any = {}) {
	        return new LinkedAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.kind = source["kind"];
	        this.parent = source["parent"];
	        this.version = source["version"];
	        this.clis = source["clis"];
	        this.agents = source["agents"];
	        this.connected = this.convertValues(source["connected"], null);
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	        this.machine = source["machine"];
	        this.role = source["role"];
	        this.activity = this.convertValues(source["activity"], CCActivity);
	        this.cliActivity = this.convertValues(source["cliActivity"], CLIActivity, true);
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
	export class LinkedSuperAI {
	    name: string;
	    url: string;
	    clis: string[];
	    reachable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LinkedSuperAI(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.clis = source["clis"];
	        this.reachable = source["reachable"];
	    }
	}
	export class LocalHive {
	    available: boolean;
	    enabled: boolean;
	    min_workers: number;
	    max_workers: number;
	    running: number;
	    live: number;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new LocalHive(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.enabled = source["enabled"];
	        this.min_workers = source["min_workers"];
	        this.max_workers = source["max_workers"];
	        this.running = source["running"];
	        this.live = source["live"];
	        this.note = source["note"];
	    }
	}
	
	export class NewIntegrationToken {
	    id: string;
	    name: string;
	    scope: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    last_used: any;
	    token: string;
	
	    static createFrom(source: any = {}) {
	        return new NewIntegrationToken(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.last_used = this.convertValues(source["last_used"], null);
	        this.token = source["token"];
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
	export class PairCode {
	    code: string;
	    // Go type: time
	    expires_at: any;
	
	    static createFrom(source: any = {}) {
	        return new PairCode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.expires_at = this.convertValues(source["expires_at"], null);
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
	export class PairedDevice {
	    id: string;
	    name: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    last_seen: any;
	
	    static createFrom(source: any = {}) {
	        return new PairedDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.last_seen = this.convertValues(source["last_seen"], null);
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
	export class PostNotificationRequest {
	    title: string;
	    body: string;
	    source: string;
	    kind: string;
	    key?: string;
	    deeplink?: string;
	    time?: string;
	    actions?: backend.NoticeAction[];
	
	    static createFrom(source: any = {}) {
	        return new PostNotificationRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.body = source["body"];
	        this.source = source["source"];
	        this.kind = source["kind"];
	        this.key = source["key"];
	        this.deeplink = source["deeplink"];
	        this.time = source["time"];
	        this.actions = this.convertValues(source["actions"], backend.NoticeAction);
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
	export class PostedNotification {
	    ok: boolean;
	    id: string;
	    duplicate: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PostedNotification(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.id = source["id"];
	        this.duplicate = source["duplicate"];
	    }
	}
	export class QueenKit {
	    systemPrompt: string;
	    servers: Record<string, any>;
	    allowed?: string[];
	    env?: Record<string, string>;
	    args?: string[];
	
	    static createFrom(source: any = {}) {
	        return new QueenKit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.systemPrompt = source["systemPrompt"];
	        this.servers = source["servers"];
	        this.allowed = source["allowed"];
	        this.env = source["env"];
	        this.args = source["args"];
	    }
	}
	export class QueenRunRequest {
	    agent: string;
	    prompt: string;
	    cwd?: string;
	    model?: string;
	    ask: boolean;
	    session?: string;
	    kit: QueenKit;
	
	    static createFrom(source: any = {}) {
	        return new QueenRunRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.prompt = source["prompt"];
	        this.cwd = source["cwd"];
	        this.model = source["model"];
	        this.ask = source["ask"];
	        this.session = source["session"];
	        this.kit = this.convertValues(source["kit"], QueenKit);
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
	export class RelayAgent {
	    name: string;
	    host: string;
	    agents: any[];
	    cliActivity: Record<string, CLIActivity>;
	
	    static createFrom(source: any = {}) {
	        return new RelayAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.agents = source["agents"];
	        this.cliActivity = this.convertValues(source["cliActivity"], CLIActivity, true);
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
	export class RemoteAgentStatus {
	    name: string;
	    about: string;
	    local: boolean;
	    hosts?: string[];
	    reachable: boolean;
	    where?: string;
	    detail?: string;
	    ms: number;
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgentStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.about = source["about"];
	        this.local = source["local"];
	        this.hosts = source["hosts"];
	        this.reachable = source["reachable"];
	        this.where = source["where"];
	        this.detail = source["detail"];
	        this.ms = source["ms"];
	    }
	}
	export class UpcomingItem {
	    kind: string;
	    id: string;
	    what: string;
	    when?: string;
	    // Go type: time
	    next?: any;
	    enabled: boolean;
	    running: boolean;
	    // Go type: time
	    lastRun?: any;
	    bee?: string;
	    waitingFor?: string;
	    schedule?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpcomingItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.id = source["id"];
	        this.what = source["what"];
	        this.when = source["when"];
	        this.next = this.convertValues(source["next"], null);
	        this.enabled = source["enabled"];
	        this.running = source["running"];
	        this.lastRun = this.convertValues(source["lastRun"], null);
	        this.bee = source["bee"];
	        this.waitingFor = source["waitingFor"];
	        this.schedule = source["schedule"];
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
	export class WakeMark {
	    agent: string;
	    kind: string;
	    reason?: string;
	    // Go type: time
	    started: any;
	    // Go type: time
	    ended?: any;
	    toolCalls: number;
	    error?: string;
	    notified: number;
	
	    static createFrom(source: any = {}) {
	        return new WakeMark(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.kind = source["kind"];
	        this.reason = source["reason"];
	        this.started = this.convertValues(source["started"], null);
	        this.ended = this.convertValues(source["ended"], null);
	        this.toolCalls = source["toolCalls"];
	        this.error = source["error"];
	        this.notified = source["notified"];
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
	
	export class APNsSettings {
	    key_path?: string;
	    key_id?: string;
	    team_id?: string;
	    topic?: string;
	    env?: string;
	
	    static createFrom(source: any = {}) {
	        return new APNsSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key_path = source["key_path"];
	        this.key_id = source["key_id"];
	        this.team_id = source["team_id"];
	        this.topic = source["topic"];
	        this.env = source["env"];
	    }
	}
	export class AgentLinkSettings {
	    relay_only?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentLinkSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.relay_only = source["relay_only"];
	    }
	}
	export class RuleScope {
	    path_prefix?: string;
	    path?: string;
	    command_prefix?: string;
	    command?: string;
	    machine?: string;
	    agent?: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleScope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path_prefix = source["path_prefix"];
	        this.path = source["path"];
	        this.command_prefix = source["command_prefix"];
	        this.command = source["command"];
	        this.machine = source["machine"];
	        this.agent = source["agent"];
	    }
	}
	export class ApprovalRule {
	    id: string;
	    // Go type: time
	    created_at: any;
	    tool: string;
	    scope: RuleScope;
	    decision: string;
	    note?: string;
	    created_from_approval_id?: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.tool = source["tool"];
	        this.scope = this.convertValues(source["scope"], RuleScope);
	        this.decision = source["decision"];
	        this.note = source["note"];
	        this.created_from_approval_id = source["created_from_approval_id"];
	        this.label = source["label"];
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
	    at?: string;
	    elapsed_ms?: number;
	    engine?: string;
	    cost_usd?: number;
	
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
	        this.at = source["at"];
	        this.elapsed_ms = source["elapsed_ms"];
	        this.engine = source["engine"];
	        this.cost_usd = source["cost_usd"];
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
	export class ExternalAgentStatus {
	    name: string;
	    installed: boolean;
	    path?: string;
	    version?: string;
	    overridden: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExternalAgentStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.installed = source["installed"];
	        this.path = source["path"];
	        this.version = source["version"];
	        this.overridden = source["overridden"];
	    }
	}
	export class ExternalAgents {
	    enabled: boolean;
	    roots?: string[];
	    unattended: boolean;
	    binaries?: Record<string, string>;
	    disabled?: string[];
	    models?: Record<string, string>;
	    timeout_seconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new ExternalAgents(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.roots = source["roots"];
	        this.unattended = source["unattended"];
	        this.binaries = source["binaries"];
	        this.disabled = source["disabled"];
	        this.models = source["models"];
	        this.timeout_seconds = source["timeout_seconds"];
	    }
	}
	export class HAStandby {
	    host: string;
	    ready: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HAStandby(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.ready = source["ready"];
	    }
	}
	export class HAService {
	    name: string;
	    provider: string;
	    vip?: string;
	    active?: string;
	    standbys: HAStandby[];
	    units: string[];
	    state: string;
	    addresses?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new HAService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.vip = source["vip"];
	        this.active = source["active"];
	        this.standbys = this.convertValues(source["standbys"], HAStandby);
	        this.units = source["units"];
	        this.state = source["state"];
	        this.addresses = source["addresses"];
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
	export class HaifySource {
	    url?: string;
	    token?: string;
	    token_file?: string;
	
	    static createFrom(source: any = {}) {
	        return new HaifySource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.token = source["token"];
	        this.token_file = source["token_file"];
	    }
	}
	export class HASettings {
	    haify?: HaifySource;
	
	    static createFrom(source: any = {}) {
	        return new HASettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.haify = this.convertValues(source["haify"], HaifySource);
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
	
	
	export class HiveMember {
	    name: string;
	    role: string;
	    url: string;
	    version?: string;
	    // Go type: time
	    joined_at: any;
	    // Go type: time
	    last_seen: any;
	    // Go type: time
	    started_at: any;
	    engine?: string;
	    node?: string;
	    kind?: string;
	    host?: string;
	    mcp: string[];
	    clis?: string[];
	    state: string;
	
	    static createFrom(source: any = {}) {
	        return new HiveMember(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.role = source["role"];
	        this.url = source["url"];
	        this.version = source["version"];
	        this.joined_at = this.convertValues(source["joined_at"], null);
	        this.last_seen = this.convertValues(source["last_seen"], null);
	        this.started_at = this.convertValues(source["started_at"], null);
	        this.engine = source["engine"];
	        this.node = source["node"];
	        this.kind = source["kind"];
	        this.host = source["host"];
	        this.mcp = source["mcp"];
	        this.clis = source["clis"];
	        this.state = source["state"];
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
	export class SpawnerSettings {
	    kind: string;
	    namespace?: string;
	    statefulset?: string;
	    max_workers?: number;
	    kubectl?: string;
	    min_workers?: number;
	    idle_minutes?: number;
	
	    static createFrom(source: any = {}) {
	        return new SpawnerSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.namespace = source["namespace"];
	        this.statefulset = source["statefulset"];
	        this.max_workers = source["max_workers"];
	        this.kubectl = source["kubectl"];
	        this.min_workers = source["min_workers"];
	        this.idle_minutes = source["idle_minutes"];
	    }
	}
	export class HiveSettings {
	    role?: string;
	    name?: string;
	    join_url?: string;
	    join_token?: string;
	    advertise_url?: string;
	    peer_token?: string;
	    spawner?: SpawnerSettings;
	    interval_seconds?: number;
	    max_concurrent_orders?: number;
	    delegation?: string;
	    delegate_timeout_seconds?: number;
	    queen_engine?: string;
	    queen_fallback?: boolean;
	    public_url?: string;
	
	    static createFrom(source: any = {}) {
	        return new HiveSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.name = source["name"];
	        this.join_url = source["join_url"];
	        this.join_token = source["join_token"];
	        this.advertise_url = source["advertise_url"];
	        this.peer_token = source["peer_token"];
	        this.spawner = this.convertValues(source["spawner"], SpawnerSettings);
	        this.interval_seconds = source["interval_seconds"];
	        this.max_concurrent_orders = source["max_concurrent_orders"];
	        this.delegation = source["delegation"];
	        this.delegate_timeout_seconds = source["delegate_timeout_seconds"];
	        this.queen_engine = source["queen_engine"];
	        this.queen_fallback = source["queen_fallback"];
	        this.public_url = source["public_url"];
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
	export class MCPCallResult {
	    ok: boolean;
	    text: string;
	    structured: any;
	    error?: string;
	    code?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPCallResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.text = source["text"];
	        this.structured = source["structured"];
	        this.error = source["error"];
	        this.code = source["code"];
	    }
	}
	export class NoticeAction {
	    label: string;
	    say: string;
	
	    static createFrom(source: any = {}) {
	        return new NoticeAction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.say = source["say"];
	    }
	}
	export class NoticeReply {
	    agent: string;
	    session: string;
	
	    static createFrom(source: any = {}) {
	        return new NoticeReply(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.session = source["session"];
	    }
	}
	export class Notification {
	    id: string;
	    level: string;
	    title?: string;
	    message: string;
	    source?: string;
	    session?: string;
	    at: string;
	    read: boolean;
	    kind?: string;
	    deeplink?: string;
	    actions?: NoticeAction[];
	    reply?: NoticeReply;
	
	    static createFrom(source: any = {}) {
	        return new Notification(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.level = source["level"];
	        this.title = source["title"];
	        this.message = source["message"];
	        this.source = source["source"];
	        this.session = source["session"];
	        this.at = source["at"];
	        this.read = source["read"];
	        this.kind = source["kind"];
	        this.deeplink = source["deeplink"];
	        this.actions = this.convertValues(source["actions"], NoticeAction);
	        this.reply = this.convertValues(source["reply"], NoticeReply);
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
	
	export class RememberOption {
	    scope: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new RememberOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.note = source["note"];
	    }
	}
	export class RemoteAgent {
	    about?: string;
	    url?: string;
	    token?: string;
	    hosts: string[];
	    probe?: string;
	    user?: string;
	    dir?: string;
	    env?: Record<string, string>;
	    command: string[];
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.about = source["about"];
	        this.url = source["url"];
	        this.token = source["token"];
	        this.hosts = source["hosts"];
	        this.probe = source["probe"];
	        this.user = source["user"];
	        this.dir = source["dir"];
	        this.env = source["env"];
	        this.command = source["command"];
	    }
	}
	export class RemoteAgents {
	    enabled: boolean;
	    agents?: Record<string, RemoteAgent>;
	    timeout_seconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new RemoteAgents(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.agents = this.convertValues(source["agents"], RemoteAgent, true);
	        this.timeout_seconds = source["timeout_seconds"];
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
	export class RemoteResult {
	    agent: string;
	    host: string;
	    text: string;
	    failed: boolean;
	    reason?: string;
	    ms: number;
	    task_id?: string;
	    usage?: domain.TokenUsage;
	    cancelled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.host = source["host"];
	        this.text = source["text"];
	        this.failed = source["failed"];
	        this.reason = source["reason"];
	        this.ms = source["ms"];
	        this.task_id = source["task_id"];
	        this.usage = this.convertValues(source["usage"], domain.TokenUsage);
	        this.cancelled = source["cancelled"];
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
	        this.err = source["err"];
	        this.rounds = source["rounds"];
	    }
	}
	export class Settings {
	    llm_base_url: string;
	    llm_key: string;
	    llm_model: string;
	    llm_context_tokens?: number;
	    llm_max_output_tokens?: number;
	    llm_reasoning_effort?: string;
	    embed_base_url: string;
	    embed_key: string;
	    embed_model: string;
	    tts_base_url: string;
	    tts_key: string;
	    tts_model: string;
	    tts_voice: string;
	    search_base_url: string;
	    search_key: string;
	    search_model: string;
	    search_native: string;
	    searxng_url: string;
	    disable_self_install: boolean;
	    disable_tool_approval: boolean;
	    tool_approval_decider_url?: string;
	    tool_approval_decider_threshold?: number;
	    workspace_dir: string;
	    max_rounds: number;
	    headless: boolean;
	    turn_max_tokens?: number;
	    long_task_max_tokens?: number;
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
	    telegram_bot_token: string;
	    telegram_allowed_chats: number[];
	    external_agents: ExternalAgents;
	    onboarded?: boolean;
	    hive: HiveSettings;
	    remote_agents: RemoteAgents;
	    action_mcp_servers?: string[];
	    apns: APNsSettings;
	    queen_name?: string;
	    queen_aliases?: string[];
	    agent_link: AgentLinkSettings;
	    ha: HASettings;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.llm_base_url = source["llm_base_url"];
	        this.llm_key = source["llm_key"];
	        this.llm_model = source["llm_model"];
	        this.llm_context_tokens = source["llm_context_tokens"];
	        this.llm_max_output_tokens = source["llm_max_output_tokens"];
	        this.llm_reasoning_effort = source["llm_reasoning_effort"];
	        this.embed_base_url = source["embed_base_url"];
	        this.embed_key = source["embed_key"];
	        this.embed_model = source["embed_model"];
	        this.tts_base_url = source["tts_base_url"];
	        this.tts_key = source["tts_key"];
	        this.tts_model = source["tts_model"];
	        this.tts_voice = source["tts_voice"];
	        this.search_base_url = source["search_base_url"];
	        this.search_key = source["search_key"];
	        this.search_model = source["search_model"];
	        this.search_native = source["search_native"];
	        this.searxng_url = source["searxng_url"];
	        this.disable_self_install = source["disable_self_install"];
	        this.disable_tool_approval = source["disable_tool_approval"];
	        this.tool_approval_decider_url = source["tool_approval_decider_url"];
	        this.tool_approval_decider_threshold = source["tool_approval_decider_threshold"];
	        this.workspace_dir = source["workspace_dir"];
	        this.max_rounds = source["max_rounds"];
	        this.headless = source["headless"];
	        this.turn_max_tokens = source["turn_max_tokens"];
	        this.long_task_max_tokens = source["long_task_max_tokens"];
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
	        this.telegram_bot_token = source["telegram_bot_token"];
	        this.telegram_allowed_chats = source["telegram_allowed_chats"];
	        this.external_agents = this.convertValues(source["external_agents"], ExternalAgents);
	        this.onboarded = source["onboarded"];
	        this.hive = this.convertValues(source["hive"], HiveSettings);
	        this.remote_agents = this.convertValues(source["remote_agents"], RemoteAgents);
	        this.action_mcp_servers = source["action_mcp_servers"];
	        this.apns = this.convertValues(source["apns"], APNsSettings);
	        this.queen_name = source["queen_name"];
	        this.queen_aliases = source["queen_aliases"];
	        this.agent_link = this.convertValues(source["agent_link"], AgentLinkSettings);
	        this.ha = this.convertValues(source["ha"], HASettings);
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
	        this.rejected = source["rejected"];
	        this.errors = source["errors"];
	        this.planDone = source["planDone"];
	        this.planTotal = source["planTotal"];
	        this.spark = source["spark"];
	    }
	}

}

export namespace domain {
	
	export class TokenUsage {
	    prompt_tokens: number;
	    completion_tokens: number;
	    cached_prompt_tokens?: number;
	    cache_write_tokens?: number;
	    reasoning_tokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new TokenUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prompt_tokens = source["prompt_tokens"];
	        this.completion_tokens = source["completion_tokens"];
	        this.cached_prompt_tokens = source["cached_prompt_tokens"];
	        this.cache_write_tokens = source["cache_write_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
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

