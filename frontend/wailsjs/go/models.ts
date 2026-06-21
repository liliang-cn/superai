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

}

export namespace backend {
	
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
	    workspace_dir: string;
	    max_rounds: number;
	    headless: boolean;
	    avatar_port: number;
	
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
	        this.workspace_dir = source["workspace_dir"];
	        this.max_rounds = source["max_rounds"];
	        this.headless = source["headless"];
	        this.avatar_port = source["avatar_port"];
	    }
	}

}

