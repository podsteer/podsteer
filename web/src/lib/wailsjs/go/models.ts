export namespace wails {
	
	export class AppInfo {
	    name: string;
	    version: string;
	    platform: string;
	    website: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.platform = source["platform"];
	        this.website = source["website"];
	    }
	}
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
	export class Event {
	    name: string;
	    namespace: string;
	    type: string;
	    isWarning: boolean;
	    reason: string;
	    message: string;
	    involvedObject: string;
	    involvedKind: string;
	    involvedName: string;
	    source: string;
	    count: number;
	    firstSeen: string;
	    lastSeen: string;
	    ageSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.type = source["type"];
	        this.isWarning = source["isWarning"];
	        this.reason = source["reason"];
	        this.message = source["message"];
	        this.involvedObject = source["involvedObject"];
	        this.involvedKind = source["involvedKind"];
	        this.involvedName = source["involvedName"];
	        this.source = source["source"];
	        this.count = source["count"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	        this.ageSeconds = source["ageSeconds"];
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
	export class Node {
	    name: string;
	    status: string;
	    roles: string[];
	    isControlPlane: boolean;
	    isHealthy: boolean;
	    unschedulable: boolean;
	    taints: number;
	    cpu: string;
	    cpuPercent: number;
	    memory: string;
	    memoryPercent: number;
	    hasMetrics: boolean;
	    version: string;
	    osImage: string;
	    architecture: string;
	    internalIp: string;
	    allocatableCpu: string;
	    allocatableMemory: string;
	    maxPods: number;
	    createdAt: string;
	    ageSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Node(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.roles = source["roles"];
	        this.isControlPlane = source["isControlPlane"];
	        this.isHealthy = source["isHealthy"];
	        this.unschedulable = source["unschedulable"];
	        this.taints = source["taints"];
	        this.cpu = source["cpu"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memory = source["memory"];
	        this.memoryPercent = source["memoryPercent"];
	        this.hasMetrics = source["hasMetrics"];
	        this.version = source["version"];
	        this.osImage = source["osImage"];
	        this.architecture = source["architecture"];
	        this.internalIp = source["internalIp"];
	        this.allocatableCpu = source["allocatableCpu"];
	        this.allocatableMemory = source["allocatableMemory"];
	        this.maxPods = source["maxPods"];
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
	    controlledBy: string;
	    qosClass: string;
	    cpu: string;
	    memory: string;
	    hasMetrics: boolean;
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
	        this.controlledBy = source["controlledBy"];
	        this.qosClass = source["qosClass"];
	        this.cpu = source["cpu"];
	        this.memory = source["memory"];
	        this.hasMetrics = source["hasMetrics"];
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
	export class ResourceKind {
	    id: string;
	    group: string;
	    version: string;
	    kind: string;
	    namespaced: boolean;
	    category: string;
	    title: string;
	    singular: string;
	    rich: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResourceKind(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.group = source["group"];
	        this.version = source["version"];
	        this.kind = source["kind"];
	        this.namespaced = source["namespaced"];
	        this.category = source["category"];
	        this.title = source["title"];
	        this.singular = source["singular"];
	        this.rich = source["rich"];
	    }
	}
	export class TableRow {
	    name: string;
	    namespace: string;
	    cells: string[];
	
	    static createFrom(source: any = {}) {
	        return new TableRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.cells = source["cells"];
	    }
	}
	export class TableColumn {
	    name: string;
	    type: string;
	    wide: boolean;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new TableColumn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.wide = source["wide"];
	        this.description = source["description"];
	    }
	}
	export class ResourceTable {
	    kindId: string;
	    title: string;
	    namespaced: boolean;
	    columns: TableColumn[];
	    rows: TableRow[];
	
	    static createFrom(source: any = {}) {
	        return new ResourceTable(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kindId = source["kindId"];
	        this.title = source["title"];
	        this.namespaced = source["namespaced"];
	        this.columns = this.convertValues(source["columns"], TableColumn);
	        this.rows = this.convertValues(source["rows"], TableRow);
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
	
	
	export class Workload {
	    kind: string;
	    name: string;
	    namespace: string;
	    status: string;
	    ready: string;
	    desired: number;
	    readyCount: number;
	    current: number;
	    updated: number;
	    available: number;
	    isHealthy: boolean;
	    isRolling: boolean;
	    suspended: boolean;
	    images: string[];
	    controlledBy: string;
	    schedule: string;
	    lastScheduled: string;
	    labels: Record<string, string>;
	    createdAt: string;
	    ageSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Workload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.status = source["status"];
	        this.ready = source["ready"];
	        this.desired = source["desired"];
	        this.readyCount = source["readyCount"];
	        this.current = source["current"];
	        this.updated = source["updated"];
	        this.available = source["available"];
	        this.isHealthy = source["isHealthy"];
	        this.isRolling = source["isRolling"];
	        this.suspended = source["suspended"];
	        this.images = source["images"];
	        this.controlledBy = source["controlledBy"];
	        this.schedule = source["schedule"];
	        this.lastScheduled = source["lastScheduled"];
	        this.labels = source["labels"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	    }
	}

}

