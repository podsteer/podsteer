export namespace wails {
	
	export class Cluster {
	    id: string;
	    server: string;
	    host: string;
	    defaultNamespace: string;
	    authInfo: string;
	    isCurrent: boolean;
	    isReachable: boolean;
	    version: string;
	    platform: string;
	
	    static createFrom(source: any = {}) {
	        return new Cluster(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.server = source["server"];
	        this.host = source["host"];
	        this.defaultNamespace = source["defaultNamespace"];
	        this.authInfo = source["authInfo"];
	        this.isCurrent = source["isCurrent"];
	        this.isReachable = source["isReachable"];
	        this.version = source["version"];
	        this.platform = source["platform"];
	    }
	}
	export class Container {
	    name: string;
	    image: string;
	    ready: boolean;
	    restartCount: number;
	    state: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new Container(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.image = source["image"];
	        this.ready = source["ready"];
	        this.restartCount = source["restartCount"];
	        this.state = source["state"];
	        this.reason = source["reason"];
	    }
	}
	export class Namespace {
	    name: string;
	    phase: string;
	    isActive: boolean;
	    createdAt: string;
	    ageSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Namespace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.phase = source["phase"];
	        this.isActive = source["isActive"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	    }
	}
	export class Pod {
	    uid: string;
	    name: string;
	    namespace: string;
	    clusterId: string;
	    phase: string;
	    statusReason: string;
	    nodeName: string;
	    podIp: string;
	    ready: string;
	    readyContainers: number;
	    totalContainers: number;
	    restarts: number;
	    isHealthy: boolean;
	    containers: Container[];
	    labels: Record<string, string>;
	    createdAt: string;
	    ageSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Pod(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uid = source["uid"];
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.clusterId = source["clusterId"];
	        this.phase = source["phase"];
	        this.statusReason = source["statusReason"];
	        this.nodeName = source["nodeName"];
	        this.podIp = source["podIp"];
	        this.ready = source["ready"];
	        this.readyContainers = source["readyContainers"];
	        this.totalContainers = source["totalContainers"];
	        this.restarts = source["restarts"];
	        this.isHealthy = source["isHealthy"];
	        this.containers = this.convertValues(source["containers"], Container);
	        this.labels = source["labels"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
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

