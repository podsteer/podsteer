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
	export class PodCapacity {
	    scheduled: number;
	    capacity: number;
	    unschedulable: number;
	    usedPercent: number;
	
	    static createFrom(source: any = {}) {
	        return new PodCapacity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scheduled = source["scheduled"];
	        this.capacity = source["capacity"];
	        this.unschedulable = source["unschedulable"];
	        this.usedPercent = source["usedPercent"];
	    }
	}
	export class ResourceUsage {
	    allocatable: string;
	    requests: string;
	    limits: string;
	    usage: string;
	    schedulable: string;
	    podUsage: string;
	    requestPercent: number;
	    limitPercent: number;
	    usagePercent: number;
	    efficiency: number;
	    measured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResourceUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allocatable = source["allocatable"];
	        this.requests = source["requests"];
	        this.limits = source["limits"];
	        this.usage = source["usage"];
	        this.schedulable = source["schedulable"];
	        this.podUsage = source["podUsage"];
	        this.requestPercent = source["requestPercent"];
	        this.limitPercent = source["limitPercent"];
	        this.usagePercent = source["usagePercent"];
	        this.efficiency = source["efficiency"];
	        this.measured = source["measured"];
	    }
	}
	export class CapacitySummary {
	    cpu: ResourceUsage;
	    memory: ResourceUsage;
	    pods: PodCapacity;
	
	    static createFrom(source: any = {}) {
	        return new CapacitySummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cpu = this.convertValues(source["cpu"], ResourceUsage);
	        this.memory = this.convertValues(source["memory"], ResourceUsage);
	        this.pods = this.convertValues(source["pods"], PodCapacity);
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
	export class Credit {
	    name: string;
	    version: string;
	    ecosystem: string;
	    licence: string;
	    copyright: string;
	    textId: string;
	
	    static createFrom(source: any = {}) {
	        return new Credit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.ecosystem = source["ecosystem"];
	        this.licence = source["licence"];
	        this.copyright = source["copyright"];
	        this.textId = source["textId"];
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
	export class Subject {
	    kind: string;
	    namespace: string;
	    name: string;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new Subject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.detail = source["detail"];
	    }
	}
	export class Finding {
	    id: string;
	    severity: string;
	    category: string;
	    title: string;
	    summary: string;
	    advice: string;
	    subjects: Subject[];
	    count: number;
	    kindId: string;
	    truncated: boolean;
	    oldestSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Finding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.severity = source["severity"];
	        this.category = source["category"];
	        this.title = source["title"];
	        this.summary = source["summary"];
	        this.advice = source["advice"];
	        this.subjects = this.convertValues(source["subjects"], Subject);
	        this.count = source["count"];
	        this.kindId = source["kindId"];
	        this.truncated = source["truncated"];
	        this.oldestSeconds = source["oldestSeconds"];
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
	export class HistorySettings {
	    retentionDays: number;
	    maxDays: number;
	    intervalSeconds: number;
	    minIntervalSeconds: number;
	    maxIntervalSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new HistorySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.retentionDays = source["retentionDays"];
	        this.maxDays = source["maxDays"];
	        this.intervalSeconds = source["intervalSeconds"];
	        this.minIntervalSeconds = source["minIntervalSeconds"];
	        this.maxIntervalSeconds = source["maxIntervalSeconds"];
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
	export class NamespaceLoad {
	    name: string;
	    pods: number;
	    notReady: number;
	    cpuRequests: string;
	    memoryRequests: string;
	    cpuUsage: string;
	    memoryUsage: string;
	    cpuShare: number;
	    memoryShare: number;
	    measured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NamespaceLoad(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.pods = source["pods"];
	        this.notReady = source["notReady"];
	        this.cpuRequests = source["cpuRequests"];
	        this.memoryRequests = source["memoryRequests"];
	        this.cpuUsage = source["cpuUsage"];
	        this.memoryUsage = source["memoryUsage"];
	        this.cpuShare = source["cpuShare"];
	        this.memoryShare = source["memoryShare"];
	        this.measured = source["measured"];
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
	export class VersionCount {
	    version: string;
	    nodes: number;
	
	    static createFrom(source: any = {}) {
	        return new VersionCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.nodes = source["nodes"];
	    }
	}
	export class NodeSummary {
	    total: number;
	    ready: number;
	    notReady: number;
	    cordoned: number;
	    underPressure: number;
	    controlPlane: number;
	    kubeletVersions: VersionCount[];
	    oldestSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.ready = source["ready"];
	        this.notReady = source["notReady"];
	        this.cordoned = source["cordoned"];
	        this.underPressure = source["underPressure"];
	        this.controlPlane = source["controlPlane"];
	        this.kubeletVersions = this.convertValues(source["kubeletVersions"], VersionCount);
	        this.oldestSeconds = source["oldestSeconds"];
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
	export class RestartHotspot {
	    namespace: string;
	    name: string;
	    restarts: number;
	    reason: string;
	    ageSeconds: number;
	    healthy: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RestartHotspot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.restarts = source["restarts"];
	        this.reason = source["reason"];
	        this.ageSeconds = source["ageSeconds"];
	        this.healthy = source["healthy"];
	    }
	}
	export class WorkloadKindSummary {
	    kind: string;
	    kindId: string;
	    title: string;
	    total: number;
	    healthy: number;
	    rolling: number;
	    degraded: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkloadKindSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.kindId = source["kindId"];
	        this.title = source["title"];
	        this.total = source["total"];
	        this.healthy = source["healthy"];
	        this.rolling = source["rolling"];
	        this.degraded = source["degraded"];
	    }
	}
	export class PodSummary {
	    total: number;
	    running: number;
	    pending: number;
	    succeeded: number;
	    failed: number;
	    terminating: number;
	    unknown: number;
	    notReady: number;
	    restarts: number;
	    bestEffort: number;
	
	    static createFrom(source: any = {}) {
	        return new PodSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.running = source["running"];
	        this.pending = source["pending"];
	        this.succeeded = source["succeeded"];
	        this.failed = source["failed"];
	        this.terminating = source["terminating"];
	        this.unknown = source["unknown"];
	        this.notReady = source["notReady"];
	        this.restarts = source["restarts"];
	        this.bestEffort = source["bestEffort"];
	    }
	}
	export class Overview {
	    clusterId: string;
	    version: string;
	    platform: string;
	    health: string;
	    generatedAt: string;
	    findings: Finding[];
	    capacity: CapacitySummary;
	    nodes: NodeSummary;
	    pods: PodSummary;
	    workloads: WorkloadKindSummary[];
	    namespaces: NamespaceLoad[];
	    restarts: RestartHotspot[];
	    unavailable: string[];
	    criticalCount: number;
	    warningCount: number;
	    infoCount: number;
	
	    static createFrom(source: any = {}) {
	        return new Overview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.clusterId = source["clusterId"];
	        this.version = source["version"];
	        this.platform = source["platform"];
	        this.health = source["health"];
	        this.generatedAt = source["generatedAt"];
	        this.findings = this.convertValues(source["findings"], Finding);
	        this.capacity = this.convertValues(source["capacity"], CapacitySummary);
	        this.nodes = this.convertValues(source["nodes"], NodeSummary);
	        this.pods = this.convertValues(source["pods"], PodSummary);
	        this.workloads = this.convertValues(source["workloads"], WorkloadKindSummary);
	        this.namespaces = this.convertValues(source["namespaces"], NamespaceLoad);
	        this.restarts = this.convertValues(source["restarts"], RestartHotspot);
	        this.unavailable = source["unavailable"];
	        this.criticalCount = source["criticalCount"];
	        this.warningCount = source["warningCount"];
	        this.infoCount = source["infoCount"];
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
	
	
	export class Sample {
	    at: number;
	    cpuUsage: number;
	    cpuRequests: number;
	    cpuAllocatable: number;
	    memoryUsage: number;
	    memoryRequests: number;
	    memoryAllocatable: number;
	    podsScheduled: number;
	    podsNotReady: number;
	    nodesReady: number;
	    nodesTotal: number;
	    measured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Sample(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.at = source["at"];
	        this.cpuUsage = source["cpuUsage"];
	        this.cpuRequests = source["cpuRequests"];
	        this.cpuAllocatable = source["cpuAllocatable"];
	        this.memoryUsage = source["memoryUsage"];
	        this.memoryRequests = source["memoryRequests"];
	        this.memoryAllocatable = source["memoryAllocatable"];
	        this.podsScheduled = source["podsScheduled"];
	        this.podsNotReady = source["podsNotReady"];
	        this.nodesReady = source["nodesReady"];
	        this.nodesTotal = source["nodesTotal"];
	        this.measured = source["measured"];
	    }
	}
	export class SeriesResult {
	    samples: Sample[];
	    spanSeconds: number;
	    retentionDays: number;
	    intervalSeconds: number;
	    recording: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SeriesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.samples = this.convertValues(source["samples"], Sample);
	        this.spanSeconds = source["spanSeconds"];
	        this.retentionDays = source["retentionDays"];
	        this.intervalSeconds = source["intervalSeconds"];
	        this.recording = source["recording"];
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

