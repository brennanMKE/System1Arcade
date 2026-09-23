export namespace game {
	
	export class Action {
	    name: string;
	    description: string;
	    buttons: string[];
	    hold_ticks: number;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.buttons = source["buttons"];
	        this.hold_ticks = source["hold_ticks"];
	    }
	}
	export class Info {
	    id: string;
	    title: string;
	    controls: string;
	    width: number;
	    height: number;
	    sprites?: Record<string, Array<string>>;
	    actions: Action[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.controls = source["controls"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.sprites = source["sprites"];
	        this.actions = this.convertValues(source["actions"], Action);
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

export namespace main {
	
	export class AgentStatus {
	    state: string;
	    detail: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.detail = source["detail"];
	        this.kind = source["kind"];
	    }
	}
	export class Settings {
	    agent: string;
	    url: string;
	    apiKey: string;
	    batch: boolean;
	    inputRate: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent = source["agent"];
	        this.url = source["url"];
	        this.apiKey = source["apiKey"];
	        this.batch = source["batch"];
	        this.inputRate = source["inputRate"];
	    }
	}
	export class Setup {
	    games: game.Info[];
	    current: string;
	    apiAddr: string;
	    apiErr: string;
	
	    static createFrom(source: any = {}) {
	        return new Setup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.games = this.convertValues(source["games"], game.Info);
	        this.current = source["current"];
	        this.apiAddr = source["apiAddr"];
	        this.apiErr = source["apiErr"];
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

