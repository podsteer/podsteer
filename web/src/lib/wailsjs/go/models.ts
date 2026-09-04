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
	export class ApplicationMember {
	    kind: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ApplicationMember(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.count = source["count"];
	    }
	}
	export class Application {
	    instance: string;
	    namespace: string;
	    partOf: string;
	    name: string;
	    managedBy: string;
	    version: string;
	    members: ApplicationMember[];
	    objects: number;
	    pods: number;
	    measuredPods: number;
	    measurablePods: number;
	    hasMetrics: boolean;
	    metricsAvailable: boolean;
	    cpu: string;
	    memory: string;
	    cpuRequest: string;
	    memoryRequest: string;
	    cpuLimit: string;
	    memoryLimit: string;
	    hasCpuRequest: boolean;
	    hasMemoryRequest: boolean;
	    hasCpuLimit: boolean;
	    hasMemoryLimit: boolean;
	    cpuPercent: number;
	    memoryPercent: number;
	    cpuLimitPercent: number;
	    memoryLimitPercent: number;
	    cpuCores: number;
	    memoryBytes: number;
	    requestCores: number;
	    requestBytes: number;
	    limitCores: number;
	    limitBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new Application(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instance = source["instance"];
	        this.namespace = source["namespace"];
	        this.partOf = source["partOf"];
	        this.name = source["name"];
	        this.managedBy = source["managedBy"];
	        this.version = source["version"];
	        this.members = this.convertValues(source["members"], ApplicationMember);
	        this.objects = source["objects"];
	        this.pods = source["pods"];
	        this.measuredPods = source["measuredPods"];
	        this.measurablePods = source["measurablePods"];
	        this.hasMetrics = source["hasMetrics"];
	        this.metricsAvailable = source["metricsAvailable"];
	        this.cpu = source["cpu"];
	        this.memory = source["memory"];
	        this.cpuRequest = source["cpuRequest"];
	        this.memoryRequest = source["memoryRequest"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryLimit = source["memoryLimit"];
	        this.hasCpuRequest = source["hasCpuRequest"];
	        this.hasMemoryRequest = source["hasMemoryRequest"];
	        this.hasCpuLimit = source["hasCpuLimit"];
	        this.hasMemoryLimit = source["hasMemoryLimit"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryPercent = source["memoryPercent"];
	        this.cpuLimitPercent = source["cpuLimitPercent"];
	        this.memoryLimitPercent = source["memoryLimitPercent"];
	        this.cpuCores = source["cpuCores"];
	        this.memoryBytes = source["memoryBytes"];
	        this.requestCores = source["requestCores"];
	        this.requestBytes = source["requestBytes"];
	        this.limitCores = source["limitCores"];
	        this.limitBytes = source["limitBytes"];
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
	export class ApplicationInventory {
	    applications: Application[];
	    unlabelled: number;
	
	    static createFrom(source: any = {}) {
	        return new ApplicationInventory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.applications = this.convertValues(source["applications"], Application);
	        this.unlabelled = source["unlabelled"];
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
	
	export class ApplyOutcomeDTO {
	    created: boolean;
	    kind: string;
	    name: string;
	    namespace: string;
	    dryRun: boolean;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ApplyOutcomeDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.created = source["created"];
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.dryRun = source["dryRun"];
	        this.warnings = source["warnings"];
	    }
	}
	export class PodCapacity {
	    scheduled: number;
	    scheduledLabel: string;
	    healthy: number;
	    healthyLabel: string;
	    capacity: number;
	    capacityLabel: string;
	    free: number;
	    freeLabel: string;
	    reserved: number;
	    reservedLabel: string;
	    reservedNodes: number;
	    unschedulable: number;
	    unschedulableLabel: string;
	    usedPercent: string;
	    freePercent: string;
	    healthyPercent: string;
	    waitingPercent: string;
	    usedPercentValue: number;
	
	    static createFrom(source: any = {}) {
	        return new PodCapacity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scheduled = source["scheduled"];
	        this.scheduledLabel = source["scheduledLabel"];
	        this.healthy = source["healthy"];
	        this.healthyLabel = source["healthyLabel"];
	        this.capacity = source["capacity"];
	        this.capacityLabel = source["capacityLabel"];
	        this.free = source["free"];
	        this.freeLabel = source["freeLabel"];
	        this.reserved = source["reserved"];
	        this.reservedLabel = source["reservedLabel"];
	        this.reservedNodes = source["reservedNodes"];
	        this.unschedulable = source["unschedulable"];
	        this.unschedulableLabel = source["unschedulableLabel"];
	        this.usedPercent = source["usedPercent"];
	        this.freePercent = source["freePercent"];
	        this.healthyPercent = source["healthyPercent"];
	        this.waitingPercent = source["waitingPercent"];
	        this.usedPercentValue = source["usedPercentValue"];
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
	    schedulablePercent: number;
	    requestPercentLabel: string;
	    usagePercentLabel: string;
	    schedulablePercentLabel: string;
	    efficiencyLabel: string;
	    efficiency: number;
	    measured: boolean;
	    reported: boolean;
	    declared: boolean;
	
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
	        this.schedulablePercent = source["schedulablePercent"];
	        this.requestPercentLabel = source["requestPercentLabel"];
	        this.usagePercentLabel = source["usagePercentLabel"];
	        this.schedulablePercentLabel = source["schedulablePercentLabel"];
	        this.efficiencyLabel = source["efficiencyLabel"];
	        this.efficiency = source["efficiency"];
	        this.measured = source["measured"];
	        this.reported = source["reported"];
	        this.declared = source["declared"];
	    }
	}
	export class CapacitySummary {
	    cpu: ResourceUsage;
	    memory: ResourceUsage;
	    ephemeral: ResourceUsage;
	    pods: PodCapacity;
	
	    static createFrom(source: any = {}) {
	        return new CapacitySummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cpu = this.convertValues(source["cpu"], ResourceUsage);
	        this.memory = this.convertValues(source["memory"], ResourceUsage);
	        this.ephemeral = this.convertValues(source["ephemeral"], ResourceUsage);
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
	export class Certificate {
	    subject: string;
	    issuer: string;
	    sans: string[];
	    notBefore: string;
	    notAfter: string;
	    expiresInSeconds: number;
	    serialNumber: string;
	    signatureAlgorithm: string;
	    publicKeyAlgorithm: string;
	    keyBits: number;
	    isCA: boolean;
	    selfSigned: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Certificate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.subject = source["subject"];
	        this.issuer = source["issuer"];
	        this.sans = source["sans"];
	        this.notBefore = source["notBefore"];
	        this.notAfter = source["notAfter"];
	        this.expiresInSeconds = source["expiresInSeconds"];
	        this.serialNumber = source["serialNumber"];
	        this.signatureAlgorithm = source["signatureAlgorithm"];
	        this.publicKeyAlgorithm = source["publicKeyAlgorithm"];
	        this.keyBits = source["keyBits"];
	        this.isCA = source["isCA"];
	        this.selfSigned = source["selfSigned"];
	    }
	}
	export class CertificateInsight {
	    severity: string;
	    title: string;
	    detail: string;
	    advice: string;
	
	    static createFrom(source: any = {}) {
	        return new CertificateInsight(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.severity = source["severity"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.advice = source["advice"];
	    }
	}
	export class CertificateChainDTO {
	    leaf: Certificate;
	    intermediates: Certificate[];
	    keyMatches?: boolean;
	    insights: CertificateInsight[];
	
	    static createFrom(source: any = {}) {
	        return new CertificateChainDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.leaf = this.convertValues(source["leaf"], Certificate);
	        this.intermediates = this.convertValues(source["intermediates"], Certificate);
	        this.keyMatches = source["keyMatches"];
	        this.insights = this.convertValues(source["insights"], CertificateInsight);
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
	export class ConditionCount {
	    condition: string;
	    nodes: number;
	
	    static createFrom(source: any = {}) {
	        return new ConditionCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.condition = source["condition"];
	        this.nodes = source["nodes"];
	    }
	}
	export class ConditionRef {
	    type: string;
	    status: string;
	    phase: string;
	
	    static createFrom(source: any = {}) {
	        return new ConditionRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.status = source["status"];
	        this.phase = source["phase"];
	    }
	}
	export class Consumer {
	    namespace: string;
	    name: string;
	    node: string;
	    usage: string;
	    request: string;
	    share: number;
	    shareLabel: string;
	    percent: number;
	
	    static createFrom(source: any = {}) {
	        return new Consumer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.name = source["name"];
	        this.node = source["node"];
	        this.usage = source["usage"];
	        this.request = source["request"];
	        this.share = source["share"];
	        this.shareLabel = source["shareLabel"];
	        this.percent = source["percent"];
	    }
	}
	export class Consumption {
	    pods: number;
	    measuredPods: number;
	    measurablePods: number;
	    hasMetrics: boolean;
	    metricsAvailable: boolean;
	    cpu: string;
	    memory: string;
	    cpuRequest: string;
	    memoryRequest: string;
	    cpuLimit: string;
	    memoryLimit: string;
	    hasCpuRequest: boolean;
	    hasMemoryRequest: boolean;
	    hasCpuLimit: boolean;
	    hasMemoryLimit: boolean;
	    cpuPercent: number;
	    memoryPercent: number;
	    cpuLimitPercent: number;
	    memoryLimitPercent: number;
	    cpuCores: number;
	    memoryBytes: number;
	    requestCores: number;
	    requestBytes: number;
	    limitCores: number;
	    limitBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new Consumption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pods = source["pods"];
	        this.measuredPods = source["measuredPods"];
	        this.measurablePods = source["measurablePods"];
	        this.hasMetrics = source["hasMetrics"];
	        this.metricsAvailable = source["metricsAvailable"];
	        this.cpu = source["cpu"];
	        this.memory = source["memory"];
	        this.cpuRequest = source["cpuRequest"];
	        this.memoryRequest = source["memoryRequest"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryLimit = source["memoryLimit"];
	        this.hasCpuRequest = source["hasCpuRequest"];
	        this.hasMemoryRequest = source["hasMemoryRequest"];
	        this.hasCpuLimit = source["hasCpuLimit"];
	        this.hasMemoryLimit = source["hasMemoryLimit"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryPercent = source["memoryPercent"];
	        this.cpuLimitPercent = source["cpuLimitPercent"];
	        this.memoryLimitPercent = source["memoryLimitPercent"];
	        this.cpuCores = source["cpuCores"];
	        this.memoryBytes = source["memoryBytes"];
	        this.requestCores = source["requestCores"];
	        this.requestBytes = source["requestBytes"];
	        this.limitCores = source["limitCores"];
	        this.limitBytes = source["limitBytes"];
	    }
	}
	export class Termination {
	    exitCode: number;
	    signal: number;
	    reason: string;
	    diagnosis: string;
	    alarming: boolean;
	    finishedAt: string;
	    lifetimeSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Termination(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exitCode = source["exitCode"];
	        this.signal = source["signal"];
	        this.reason = source["reason"];
	        this.diagnosis = source["diagnosis"];
	        this.alarming = source["alarming"];
	        this.finishedAt = source["finishedAt"];
	        this.lifetimeSeconds = source["lifetimeSeconds"];
	    }
	}
	export class Container {
	    name: string;
	    image: string;
	    ready: boolean;
	    restartCount: number;
	    state: string;
	    reason: string;
	    started: boolean;
	    tty: boolean;
	    stdin: boolean;
	    requests: string;
	    limits: string;
	    cpu: string;
	    memory: string;
	    hasMetrics: boolean;
	    lastTermination?: Termination;
	
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
	        this.started = source["started"];
	        this.tty = source["tty"];
	        this.stdin = source["stdin"];
	        this.requests = source["requests"];
	        this.limits = source["limits"];
	        this.cpu = source["cpu"];
	        this.memory = source["memory"];
	        this.hasMetrics = source["hasMetrics"];
	        this.lastTermination = this.convertValues(source["lastTermination"], Termination);
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
	export class Credit {
	    name: string;
	    version: string;
	    ecosystem: string;
	    licence: string;
	    copyright: string;
	    textId: string;
	    noticeTextId: string;
	    expression: string;
	
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
	        this.noticeTextId = source["noticeTextId"];
	        this.expression = source["expression"];
	    }
	}
	export class DiskSummary {
	    measured: number;
	    fullestPercent: number;
	    fullestNode: string;
	    filling: number;
	
	    static createFrom(source: any = {}) {
	        return new DiskSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.measured = source["measured"];
	        this.fullestPercent = source["fullestPercent"];
	        this.fullestNode = source["fullestNode"];
	        this.filling = source["filling"];
	    }
	}
	export class DrainFailure {
	    pod: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new DrainFailure(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pod = source["pod"];
	        this.reason = source["reason"];
	    }
	}
	export class DrainSkip {
	    pod: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new DrainSkip(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pod = source["pod"];
	        this.reason = source["reason"];
	    }
	}
	export class DrainPlanDTO {
	    evict: string[];
	    skipped: DrainSkip[];
	    refused: DrainSkip[];
	    runnable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DrainPlanDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.evict = source["evict"];
	        this.skipped = this.convertValues(source["skipped"], DrainSkip);
	        this.refused = this.convertValues(source["refused"], DrainSkip);
	        this.runnable = source["runnable"];
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
	export class DrainReportDTO {
	    cordoned: boolean;
	    evicted: string[];
	    skipped: DrainSkip[];
	    refused: DrainSkip[];
	    failed: DrainFailure[];
	    timedOut: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DrainReportDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cordoned = source["cordoned"];
	        this.evicted = source["evicted"];
	        this.skipped = this.convertValues(source["skipped"], DrainSkip);
	        this.refused = this.convertValues(source["refused"], DrainSkip);
	        this.failed = this.convertValues(source["failed"], DrainFailure);
	        this.timedOut = source["timedOut"];
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
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
	
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
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
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
	export class GraphEdge {
	    from: string;
	    to: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphEdge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.label = source["label"];
	    }
	}
	export class GraphNode {
	    id: string;
	    kind: string;
	    apiKind: string;
	    name: string;
	    namespace: string;
	    tier: number;
	    detail: string;
	    healthy: boolean;
	    subject: boolean;
	    group: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.apiKind = source["apiKind"];
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.tier = source["tier"];
	        this.detail = source["detail"];
	        this.healthy = source["healthy"];
	        this.subject = source["subject"];
	        this.group = source["group"];
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
	export class KubeconfigMerge {
	    added: string[];
	    conflicts: string[];
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new KubeconfigMerge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.added = source["added"];
	        this.conflicts = source["conflicts"];
	        this.path = source["path"];
	    }
	}
	export class MetricsBackend {
	    kind: string;
	    label: string;
	    namespace: string;
	    service: string;
	    port: string;
	
	    static createFrom(source: any = {}) {
	        return new MetricsBackend(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.namespace = source["namespace"];
	        this.service = source["service"];
	        this.port = source["port"];
	    }
	}
	export class Namespace {
	    name: string;
	    phase: string;
	    isActive: boolean;
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
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
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	    }
	}
	export class ResourceCount {
	    kindId: string;
	    kind: string;
	    title: string;
	    count: number;
	    unreadable: string;
	
	    static createFrom(source: any = {}) {
	        return new ResourceCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kindId = source["kindId"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.count = source["count"];
	        this.unreadable = source["unreadable"];
	    }
	}
	export class NamespaceInventory {
	    namespace: string;
	    counts: ResourceCount[];
	    empty: number;
	    total: number;
	    unreadable: number;
	
	    static createFrom(source: any = {}) {
	        return new NamespaceInventory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.namespace = source["namespace"];
	        this.counts = this.convertValues(source["counts"], ResourceCount);
	        this.empty = source["empty"];
	        this.total = source["total"];
	        this.unreadable = source["unreadable"];
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
	export class NamespaceSummary {
	    name: string;
	    phase: string;
	    isActive: boolean;
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
	    createdAt: string;
	    ageSeconds: number;
	    notReady: number;
	    pods: number;
	    measuredPods: number;
	    measurablePods: number;
	    hasMetrics: boolean;
	    metricsAvailable: boolean;
	    cpu: string;
	    memory: string;
	    cpuRequest: string;
	    memoryRequest: string;
	    cpuLimit: string;
	    memoryLimit: string;
	    hasCpuRequest: boolean;
	    hasMemoryRequest: boolean;
	    hasCpuLimit: boolean;
	    hasMemoryLimit: boolean;
	    cpuPercent: number;
	    memoryPercent: number;
	    cpuLimitPercent: number;
	    memoryLimitPercent: number;
	    cpuCores: number;
	    memoryBytes: number;
	    requestCores: number;
	    requestBytes: number;
	    limitCores: number;
	    limitBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new NamespaceSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.phase = source["phase"];
	        this.isActive = source["isActive"];
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	        this.notReady = source["notReady"];
	        this.pods = source["pods"];
	        this.measuredPods = source["measuredPods"];
	        this.measurablePods = source["measurablePods"];
	        this.hasMetrics = source["hasMetrics"];
	        this.metricsAvailable = source["metricsAvailable"];
	        this.cpu = source["cpu"];
	        this.memory = source["memory"];
	        this.cpuRequest = source["cpuRequest"];
	        this.memoryRequest = source["memoryRequest"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryLimit = source["memoryLimit"];
	        this.hasCpuRequest = source["hasCpuRequest"];
	        this.hasMemoryRequest = source["hasMemoryRequest"];
	        this.hasCpuLimit = source["hasCpuLimit"];
	        this.hasMemoryLimit = source["hasMemoryLimit"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryPercent = source["memoryPercent"];
	        this.cpuLimitPercent = source["cpuLimitPercent"];
	        this.memoryLimitPercent = source["memoryLimitPercent"];
	        this.cpuCores = source["cpuCores"];
	        this.memoryBytes = source["memoryBytes"];
	        this.requestCores = source["requestCores"];
	        this.requestBytes = source["requestBytes"];
	        this.limitCores = source["limitCores"];
	        this.limitBytes = source["limitBytes"];
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
	    disk: string;
	    diskCapacity: string;
	    diskPercent: number;
	    hasDisk: boolean;
	    maxPods: number;
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
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
	        this.disk = source["disk"];
	        this.diskCapacity = source["diskCapacity"];
	        this.diskPercent = source["diskPercent"];
	        this.hasDisk = source["hasDisk"];
	        this.maxPods = source["maxPods"];
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	    }
	}
	export class NodeLoad {
	    name: string;
	    ready: boolean;
	    schedulable: boolean;
	    controlPlane: boolean;
	    reserved: boolean;
	    cpuPercent: number;
	    memoryPercent: number;
	    podPercent: number;
	    diskPercent: number;
	    pods: number;
	    usageCpuMilli: number;
	    usageMemoryBytes: number;
	    usageMeasured: boolean;
	    cpuAmount: string;
	    memoryAmount: string;
	    podAmount: string;
	    diskAmount: string;
	    cpuShare: string;
	    memoryShare: string;
	    podShare: string;
	    diskShare: string;
	    diskMeasured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NodeLoad(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ready = source["ready"];
	        this.schedulable = source["schedulable"];
	        this.controlPlane = source["controlPlane"];
	        this.reserved = source["reserved"];
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryPercent = source["memoryPercent"];
	        this.podPercent = source["podPercent"];
	        this.diskPercent = source["diskPercent"];
	        this.pods = source["pods"];
	        this.usageCpuMilli = source["usageCpuMilli"];
	        this.usageMemoryBytes = source["usageMemoryBytes"];
	        this.usageMeasured = source["usageMeasured"];
	        this.cpuAmount = source["cpuAmount"];
	        this.memoryAmount = source["memoryAmount"];
	        this.podAmount = source["podAmount"];
	        this.diskAmount = source["diskAmount"];
	        this.cpuShare = source["cpuShare"];
	        this.memoryShare = source["memoryShare"];
	        this.podShare = source["podShare"];
	        this.diskShare = source["diskShare"];
	        this.diskMeasured = source["diskMeasured"];
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
	    schedulable: number;
	    tainted: number;
	    pressure: ConditionCount[];
	    disks: DiskSummary;
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
	        this.schedulable = source["schedulable"];
	        this.tainted = source["tainted"];
	        this.pressure = this.convertValues(source["pressure"], ConditionCount);
	        this.disks = this.convertValues(source["disks"], DiskSummary);
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
	export class UpgradeSummary {
	    targetMinor: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new UpgradeSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetMinor = source["targetMinor"];
	        this.count = source["count"];
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
	export class ReleaseSupport {
	    minor: string;
	    state: string;
	    endOfLife: string;
	    days: number;
	    compiledAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseSupport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.minor = source["minor"];
	        this.state = source["state"];
	        this.endOfLife = source["endOfLife"];
	        this.days = source["days"];
	        this.compiledAt = source["compiledAt"];
	    }
	}
	export class TopConsumers {
	    byCpu: Consumer[];
	    byMemory: Consumer[];
	    measured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TopConsumers(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.byCpu = this.convertValues(source["byCpu"], Consumer);
	        this.byMemory = this.convertValues(source["byMemory"], Consumer);
	        this.measured = source["measured"];
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
	export class StorageClassUsage {
	    name: string;
	    volumes: number;
	    size: string;
	    share: number;
	
	    static createFrom(source: any = {}) {
	        return new StorageClassUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.volumes = source["volumes"];
	        this.size = source["size"];
	        this.share = source["share"];
	    }
	}
	export class PhaseCount {
	    phase: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new PhaseCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.count = source["count"];
	    }
	}
	export class StorageSummary {
	    provisioned: string;
	    unbound: string;
	    orphaned: string;
	    orphanedBytes: number;
	    claims: PhaseCount[];
	    volumes: PhaseCount[];
	    totalClaims: number;
	    totalVolumes: number;
	    largest: string;
	    largestName: string;
	    largestBytes: number;
	    unboundBytes: number;
	    classes: StorageClassUsage[];
	
	    static createFrom(source: any = {}) {
	        return new StorageSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provisioned = source["provisioned"];
	        this.unbound = source["unbound"];
	        this.orphaned = source["orphaned"];
	        this.orphanedBytes = source["orphanedBytes"];
	        this.claims = this.convertValues(source["claims"], PhaseCount);
	        this.volumes = this.convertValues(source["volumes"], PhaseCount);
	        this.totalClaims = source["totalClaims"];
	        this.totalVolumes = source["totalVolumes"];
	        this.largest = source["largest"];
	        this.largestName = source["largestName"];
	        this.largestBytes = source["largestBytes"];
	        this.unboundBytes = source["unboundBytes"];
	        this.classes = this.convertValues(source["classes"], StorageClassUsage);
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
	export class Overview {
	    clusterId: string;
	    version: string;
	    platform: string;
	    health: string;
	    generatedAt: string;
	    findings: Finding[];
	    capacity: CapacitySummary;
	    nodes: NodeSummary;
	    storage: StorageSummary;
	    consumers: TopConsumers;
	    support: ReleaseSupport;
	    nodeLoads: NodeLoad[];
	    pods: PodSummary;
	    workloads: WorkloadKindSummary[];
	    namespaces: NamespaceLoad[];
	    restarts: RestartHotspot[];
	    unavailable: string[];
	    metrics: string;
	    backend: MetricsBackend;
	    criticalCount: number;
	    warningCount: number;
	    infoCount: number;
	    upgrade: UpgradeSummary;
	    knownMinors: string[];
	
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
	        this.storage = this.convertValues(source["storage"], StorageSummary);
	        this.consumers = this.convertValues(source["consumers"], TopConsumers);
	        this.support = this.convertValues(source["support"], ReleaseSupport);
	        this.nodeLoads = this.convertValues(source["nodeLoads"], NodeLoad);
	        this.pods = this.convertValues(source["pods"], PodSummary);
	        this.workloads = this.convertValues(source["workloads"], WorkloadKindSummary);
	        this.namespaces = this.convertValues(source["namespaces"], NamespaceLoad);
	        this.restarts = this.convertValues(source["restarts"], RestartHotspot);
	        this.unavailable = source["unavailable"];
	        this.metrics = source["metrics"];
	        this.backend = this.convertValues(source["backend"], MetricsBackend);
	        this.criticalCount = source["criticalCount"];
	        this.warningCount = source["warningCount"];
	        this.infoCount = source["infoCount"];
	        this.upgrade = this.convertValues(source["upgrade"], UpgradeSummary);
	        this.knownMinors = source["knownMinors"];
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
	
	export class PodFinding {
	    severity: string;
	    title: string;
	    detail: string;
	    advice: string;
	
	    static createFrom(source: any = {}) {
	        return new PodFinding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.severity = source["severity"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.advice = source["advice"];
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
	    cpuPercent: number;
	    memoryPercent: number;
	    cpuRequest: string;
	    memoryRequest: string;
	    hasCpuRequest: boolean;
	    hasMemoryRequest: boolean;
	    cpuLimitPercent: number;
	    memoryLimitPercent: number;
	    cpuLimit: string;
	    memoryLimit: string;
	    hasCpuLimit: boolean;
	    hasMemoryLimit: boolean;
	    containers: Container[];
	    findings: PodFinding[];
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
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
	        this.cpuPercent = source["cpuPercent"];
	        this.memoryPercent = source["memoryPercent"];
	        this.cpuRequest = source["cpuRequest"];
	        this.memoryRequest = source["memoryRequest"];
	        this.hasCpuRequest = source["hasCpuRequest"];
	        this.hasMemoryRequest = source["hasMemoryRequest"];
	        this.cpuLimitPercent = source["cpuLimitPercent"];
	        this.memoryLimitPercent = source["memoryLimitPercent"];
	        this.cpuLimit = source["cpuLimit"];
	        this.memoryLimit = source["memoryLimit"];
	        this.hasCpuLimit = source["hasCpuLimit"];
	        this.hasMemoryLimit = source["hasMemoryLimit"];
	        this.containers = this.convertValues(source["containers"], Container);
	        this.findings = this.convertValues(source["findings"], PodFinding);
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
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
	
	
	export class PodGraph {
	    nodes: GraphNode[];
	    edges: GraphEdge[];
	    unreadable: string[];
	
	    static createFrom(source: any = {}) {
	        return new PodGraph(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodes = this.convertValues(source["nodes"], GraphNode);
	        this.edges = this.convertValues(source["edges"], GraphEdge);
	        this.unreadable = source["unreadable"];
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
	
	export class PortForward {
	    id: string;
	    clusterId: string;
	    namespace: string;
	    pod: string;
	    localPort: number;
	    remotePort: number;
	    address: string;
	    scheme: string;
	    reconnecting: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PortForward(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.clusterId = source["clusterId"];
	        this.namespace = source["namespace"];
	        this.pod = source["pod"];
	        this.localPort = source["localPort"];
	        this.remotePort = source["remotePort"];
	        this.address = source["address"];
	        this.scheme = source["scheme"];
	        this.reconnecting = source["reconnecting"];
	    }
	}
	
	
	export class ResourceKind {
	    id: string;
	    group: string;
	    version: string;
	    kind: string;
	    namespaced: boolean;
	    category: string;
	    subcategory: string;
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
	        this.subcategory = source["subcategory"];
	        this.title = source["title"];
	        this.singular = source["singular"];
	        this.rich = source["rich"];
	    }
	}
	export class TableRow {
	    name: string;
	    namespace: string;
	    cells: string[];
	    labels: Record<string, string>;
	    annotations: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new TableRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.namespace = source["namespace"];
	        this.cells = source["cells"];
	        this.labels = source["labels"];
	        this.annotations = source["annotations"];
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
	
	
	export class RevisionDTO {
	    number: number;
	    name: string;
	    createdAt: string;
	    ageSeconds: number;
	    current: boolean;
	    replicas: number;
	    images: string[];
	    changeCause: string;
	    templateYaml: string;
	
	    static createFrom(source: any = {}) {
	        return new RevisionDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.number = source["number"];
	        this.name = source["name"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	        this.current = source["current"];
	        this.replicas = source["replicas"];
	        this.images = source["images"];
	        this.changeCause = source["changeCause"];
	        this.templateYaml = source["templateYaml"];
	    }
	}
	export class RollbackOutcomeDTO {
	    toRevision: number;
	    dryRun: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RollbackOutcomeDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.toRevision = source["toRevision"];
	        this.dryRun = source["dryRun"];
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
	
	
	
	
	
	
	
	export class UpdateStatus {
	    state: string;
	    installed: string;
	    latest: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.installed = source["installed"];
	        this.latest = source["latest"];
	        this.url = source["url"];
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
	    annotations: Record<string, string>;
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
	        this.annotations = source["annotations"];
	        this.createdAt = source["createdAt"];
	        this.ageSeconds = source["ageSeconds"];
	    }
	}

}

