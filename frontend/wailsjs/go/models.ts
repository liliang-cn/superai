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
	export class Settings {
	    llm_base_url: string;
	    llm_key: string;
	    llm_model: string;
	    embed_base_url: string;
	    embed_key: string;
	    embed_model: string;
	    search_base_url: string;
	    search_key: string;
	    search_model: string;
	    disable_self_install: boolean;
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
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.llm_base_url = source["llm_base_url"];
	        this.llm_key = source["llm_key"];
	        this.llm_model = source["llm_model"];
	        this.embed_base_url = source["embed_base_url"];
	        this.embed_key = source["embed_key"];
	        this.embed_model = source["embed_model"];
	        this.search_base_url = source["search_base_url"];
	        this.search_key = source["search_key"];
	        this.search_model = source["search_model"];
	        this.disable_self_install = source["disable_self_install"];
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

