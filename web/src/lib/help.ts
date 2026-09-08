/**
 * What each dialog is FOR, in the operator's words rather than Kubernetes'.
 *
 * WHY A REGISTRY AND NOT PROSE IN THE DIALOGS. Every one of these dialogs
 * used to print its explanation in full, every time it opened — paragraphs
 * somebody reads once, if ever, standing between them and the button they came
 * to press. The dialogs now say what CHANGES between one opening and the next
 * (which object, which namespace, which revision) and keep the reasoning here,
 * one press of (?) away, in a panel that opens beside them.
 *
 * WHAT BELONGS HERE is what is true every time: what the action does to the
 * cluster, what it does NOT do, what refuses it and why. What does not belong
 * here is anything about the object in front of the operator — that is the
 * dialog's own job, and a help panel repeating it would go stale the moment
 * they opened a different one.
 *
 * Sections are short and headed, because this is read standing up, mid-task,
 * by somebody who wants one answer and not a manual.
 */

/** One headed run of paragraphs. */
export interface HelpSection {
  heading: string
  /** Paragraphs, in order. Plain sentences — the panel renders text, not markup. */
  body: string[]
}

export interface HelpTopic {
  /** Names the panel. Matches the dialog it opens from. */
  title: string
  /** One sentence, before the sections, saying what this is. */
  lede: string
  sections: HelpSection[]
}

/**
 * Every topic, keyed by id.
 *
 * `satisfies` rather than an annotation, so `HelpTopicId` below is the union
 * of the ids that actually exist: a dialog naming a topic nobody wrote fails
 * to compile rather than opening an empty panel.
 */
export const HELP_TOPICS = {
  'local-shell': {
    title: 'Local terminal',
    lede: 'Your own login shell, on this machine, pointed at the cluster this tab has open.',
    sections: [
      {
        heading: 'What it is',
        body: [
          'A shell on your machine, in your home directory, with KUBECONFIG set to the same files PodSteer reads. kubectl and helm see the same clusters you do — whichever versions you already have installed. PodSteer installs nothing and downloads nothing.',
        ],
      },
      {
        heading: 'Your kubeconfig is not rewritten',
        body: [
          'The shell is told which context the open tab is on, but current-context in your kubeconfig is left exactly as it is, so pass --context when you want this cluster.',
          'That is deliberate: kubectl in your other terminals must not change target because you opened a pane here.',
        ],
      },
      {
        heading: 'Read-only does not apply here',
        body: [
          "PodSteer's read-only setting guards PodSteer's own writes to a cluster. A shell you opened yourself, holding your own credentials, is not something this application can or should police — what you can do in it is whatever your kubeconfig grants.",
        ],
      },
      {
        heading: 'Starting a coding agent instead',
        body: [
          'If you have one on your PATH, PodSteer can start it in the same shell rather than your login shell, and tell it which cluster and object you had open.',
          'The read-only tick is a request in its opening prompt, not a restriction. The agent runs with your credentials and nothing here can narrow them. Nothing is sent anywhere by PodSteer; this starts a process on this machine.',
        ],
      },
    ],
  },

  'cluster-shell': {
    title: 'In-cluster shell',
    lede: "A throwaway pod in the cluster, so kubectl, dig and curl see the cluster's network from the inside.",
    sections: [
      {
        heading: 'What it is',
        body: [
          "An ordinary, unprivileged pod that PodSteer creates in a namespace and attaches you to — not a debug container on somebody else's pod, and not a root shell on a node.",
          'It is the vantage point that matters: DNS, Services and NetworkPolicies resolve the way they do for a workload, which is what makes it the place to test them from.',
        ],
      },
      {
        heading: 'What it can do',
        body: [
          "The pod mounts the namespace's default ServiceAccount token, so kubectl inside it carries that namespace's permissions — the same ones any pod there would have, and none of yours.",
        ],
      },
      {
        heading: 'How long it lives',
        body: [
          'It is deleted when you close its terminal, and self-destructs after an hour as a backstop. While it runs it appears in the activity list, where it can also be stopped.',
        ],
      },
      {
        heading: 'If admission refuses it',
        body: [
          "The pod asks to run as non-root, with no privilege escalation, every capability dropped and the runtime's default seccomp profile, so it is admitted in a namespace enforcing Pod Security's restricted profile.",
          "If admission still refuses, the message you get is the API server's own — a policy problem to take to whoever set the policy, not a permissions problem with your account.",
        ],
      },
    ],
  },

  'node-shell': {
    title: 'Node shell',
    lede: "A root shell on the node itself, in the host's own namespaces.",
    sections: [
      {
        heading: 'What it is',
        body: [
          "A privileged pod pinned to this node that enters PID 1's process, network and mount namespaces with nsenter. Anything you can do on the node's own console, you can do here.",
          "It is not sandboxed and it is not an ordinary pod. Reach for the in-cluster shell instead when what you want is the cluster's network rather than the machine.",
        ],
      },
      {
        heading: 'How long it lives',
        body: [
          'The pod is deleted when you close its terminal, and self-destructs after an hour as a backstop. While it runs it appears in the activity list, where it can also be stopped.',
        ],
      },
      {
        heading: 'Where it lands',
        body: [
          'It tolerates every taint, because it has to reach one specific node — including control-plane and otherwise reserved nodes.',
        ],
      },
    ],
  },

  debug: {
    title: 'Debug container',
    lede: 'An ephemeral container added to a running pod, sharing its namespaces — what kubectl debug does.',
    sections: [
      {
        heading: 'What it is',
        body: [
          'A container added to a pod that is already running, with a toolbox image of your choosing. It is the way into a distroless or scratch container that has no shell of its own.',
          'Targeting a container shares its process namespace, so you can see and trace its processes from the debug container.',
        ],
      },
      {
        heading: 'What it changes',
        body: [
          'It modifies the pod: an ephemeral container cannot be removed once added, and it stays on the pod until the pod is replaced. Nothing restarts, and the existing containers are untouched.',
        ],
      },
    ],
  },

  delete: {
    title: 'Delete',
    lede: 'Removes the object from the cluster.',
    sections: [
      {
        heading: 'What happens',
        body: [
          "The object is deleted through the API, with the cluster's own cascade: deleting a Deployment takes its ReplicaSets and their pods, and deleting a namespace takes everything in it.",
          'Anything a controller owns comes back. Deleting a pod that a Deployment manages gets you a new pod, not fewer pods — scale or delete the workload instead.',
        ],
      },
      {
        heading: 'On a production cluster',
        body: [
          'When the cluster is in a group you marked production, PodSteer asks you to type the name first. It is the one gesture that cannot be made by a mis-click on the wrong row.',
        ],
      },
    ],
  },

  evict: {
    title: 'Evict',
    lede: 'Asks a pod to leave through the eviction API — the respectful removal a drain uses.',
    sections: [
      {
        heading: 'Not the same as a delete',
        body: [
          'An eviction goes through the same API a drain uses, so the cluster gets to refuse it. A delete does not ask.',
        ],
      },
      {
        heading: 'What refuses it',
        body: [
          "A PodDisruptionBudget on this pod's workload refuses the eviction if letting the pod go would leave too few replicas running. The refusal is the budget working, not a failure — it is telling you the workload cannot spare this pod right now.",
        ],
      },
      {
        heading: 'Why there is no kubectl line for it',
        body: [
          'Every other dialog here shows the kubectl command it is the graphical form of. This one does not, because kubectl has no eviction verb: eviction is an API call, and the only kubectl command that makes one is drain, which evicts everything on a node.',
          'kubectl delete pod is not the same act and is not offered as a substitute — a delete does not ask the PodDisruptionBudget, so a command that looks equivalent would take away the protection the eviction exists to respect.',
        ],
      },
    ],
  },

  drain: {
    title: 'Drain',
    lede: 'Cordons a node and evicts what is running on it, so the machine can be taken away.',
    sections: [
      {
        heading: 'The plan comes first',
        body: [
          'PodSteer runs the same plan the drain will run and shows it before you commit, so the number of pods it will evict is never a guess. If the plan is not runnable, the button is disabled with the reason — the same refusal kubectl drain would give, seen before the click rather than after.',
        ],
      },
      {
        heading: 'What holds a drain up',
        body: [
          'Pods no controller owns are not recreated once evicted, so a drain refuses them unless you say to go ahead anyway.',
          'Pods with local storage lose what is in emptyDir volumes, so those are refused separately.',
          'DaemonSet pods are never evicted: their controller would put them straight back.',
        ],
      },
      {
        heading: 'Afterwards',
        body: [
          'The node stays cordoned when the drain finishes. Uncordon it when the machine is back in service, or nothing will schedule there again.',
        ],
      },
    ],
  },

  cordon: {
    title: 'Cordon',
    lede: 'Marks a node unschedulable without touching what already runs on it.',
    sections: [
      {
        heading: 'What it does',
        body: [
          'New pods will not be placed here. Pods already running stay running, and nothing moves.',
          'It is the first half of a drain, and useful on its own to stop a suspect node taking new work while you look at it.',
        ],
      },
      {
        heading: 'Undoing it',
        body: [
          'Uncordon makes the node schedulable again. The scheduler does not rebalance what is already placed elsewhere — pods arrive as workloads are created or replaced.',
        ],
      },
    ],
  },

  restart: {
    title: 'Restart',
    lede: 'Rolls every pod of the workload, the way kubectl rollout restart does.',
    sections: [
      {
        heading: 'What happens',
        body: [
          "PodSteer stamps the pod template with a restart annotation, which makes the controller roll out new pods under the workload's own update strategy — surge, maxUnavailable and readiness gates all apply.",
          'Nothing about the workload changes: same image, same configuration, new pods.',
        ],
      },
      {
        heading: 'When it is the right tool',
        body: [
          'Picking up a changed ConfigMap or Secret that the pods read at start-up, or clearing state a process has got itself into. It is not a fix for a crash loop, which comes back with the new pods.',
        ],
      },
    ],
  },

  scale: {
    title: 'Scale',
    lede: 'Sets how many replicas the workload should run.',
    sections: [
      {
        heading: 'What happens',
        body: [
          'The controller creates or removes pods until the count matches. Removing them follows the same eviction rules a drain does, so a PodDisruptionBudget can hold a scale-down up.',
        ],
      },
      {
        heading: 'If an autoscaler owns this workload',
        body: [
          'A HorizontalPodAutoscaler targeting this workload will move the count back to what its own metrics say, usually within a minute. PodSteer says so when it finds one: the number you set here is a suggestion the autoscaler is free to overrule.',
        ],
      },
      {
        heading: 'Scaling to zero',
        body: [
          'Zero takes the workload off the air entirely while leaving it in the cluster, so on a production cluster it costs the same type-the-name confirmation a delete does. Any other number stays one click: it changes capacity, not whether the workload exists.',
        ],
      },
    ],
  },

  'set-image': {
    title: 'Set image',
    lede: "Changes the image on one or more of the workload's containers.",
    sections: [
      {
        heading: 'What happens',
        body: [
          'Each container you changed is written separately, in template order, and the controller rolls the workload out under its own update strategy.',
          'A field you did not change produces no write at all — the same image written again would still roll every pod.',
        ],
      },
      {
        heading: 'If one of them fails',
        body: [
          'PodSteer stops at the first failure and tells you exactly which containers were changed before it. A rollout is not atomic either way, so stopping early leaves less to reason about than pressing on.',
        ],
      },
    ],
  },

  rollback: {
    title: 'Rollback',
    lede: 'Puts the workload back on an earlier revision, the way kubectl rollout undo does.',
    sections: [
      {
        heading: 'Preview runs the real thing',
        body: [
          "Preview makes the same call the rollback makes, with dryRun set, so the answer comes from the API server's own admission chain rather than from a guess about it. What preview accepts is what the rollback will do.",
        ],
      },
      {
        heading: 'What a revision holds',
        body: [
          'The pod template as it was — image, command, environment, resources. It does not hold the replica count, or anything outside the workload: a ConfigMap the old pods read has not moved back, and neither has anything the workload depends on.',
        ],
      },
    ],
  },

  suspend: {
    title: 'Suspend',
    lede: 'Stops a CronJob scheduling, or stops a Job running, without deleting it.',
    sections: [
      {
        heading: 'A CronJob',
        body: [
          'Scheduled runs stop until it is resumed. A run already in progress finishes — suspending does not reach into a Job that is already going.',
          'Missed schedules are not made up when you resume: the next run is the next one due.',
        ],
      },
      {
        heading: 'A Job',
        body: [
          'Suspending deletes its active pods. Resuming starts them again from scratch, with the retry count reset — anything the pods had done and not recorded somewhere is gone.',
        ],
      },
    ],
  },

  trigger: {
    title: 'Run now',
    lede: "Creates a Job from the CronJob's template, outside its schedule.",
    sections: [
      {
        heading: 'What happens',
        body: [
          'A Job is created from the template as it is now, and runs immediately. It appears under the CronJob and counts towards its history limits, so it can push an older run out of the list.',
          'The schedule is untouched: the next scheduled run still happens when it was going to.',
        ],
      },
      {
        heading: 'Concurrency still applies',
        body: [
          'A CronJob with concurrencyPolicy Forbid will not start this run while another is going, and one set to Replace will stop the running one.',
        ],
      },
    ],
  },

  'rollout-action': {
    title: 'Pause and resume',
    lede: "Holds a workload's rollout where it is, or lets it continue.",
    sections: [
      {
        heading: 'Pausing',
        body: [
          'The controller stops acting on changes to the pod template. Pods already updated stay updated and pods not yet updated stay as they are, so a rollout can be stopped part-way while you look at what the new pods are doing.',
          'Edits made while paused accumulate; they all take effect at once when you resume.',
        ],
      },
      {
        heading: 'Resuming',
        body: [
          "The rollout continues under the workload's update strategy. To go back instead of forward, resume and then roll back.",
        ],
      },
    ],
  },

  'bulk-action': {
    title: 'Acting on several objects',
    lede: 'The same action, applied to everything you selected.',
    sections: [
      {
        heading: 'One at a time, reported one at a time',
        body: [
          'PodSteer makes one call per object and tells you what happened to each. There is no transaction: some can succeed while others are refused, and stopping half way through leaves the ones already done as they are.',
        ],
      },
      {
        heading: 'What can refuse',
        body: [
          'Each object goes through the same guards it would on its own — read-only clusters, PodDisruptionBudgets, admission policies and RBAC. A refusal on one object says nothing about the others.',
        ],
      },
    ],
  },

  'add-cluster': {
    title: 'Adding a cluster',
    lede: 'Points PodSteer at a kubeconfig context.',
    sections: [
      {
        heading: 'Where clusters come from',
        body: [
          'PodSteer reads the kubeconfig files your shell already uses, and every context in them is a cluster it can open. It does not store credentials of its own and never writes to those files.',
        ],
      },
      {
        heading: 'Connecting through a proxy or a jump host',
        body: [
          'Whatever your kubeconfig says — exec plugins, proxy URLs, client certificates — is what PodSteer uses, because it is the same client library kubectl is. If kubectl reaches the cluster from this machine, so does PodSteer.',
        ],
      },
    ],
  },

  'create-resource': {
    title: 'Create from YAML',
    lede: 'Sends a manifest to the API server, the way kubectl apply does.',
    sections: [
      {
        heading: 'What happens',
        body: [
          'The document is sent as written. The API server validates it, applies defaults and runs admission — so what comes back is the object as the cluster made it, which is not always the object you sent.',
          'A document naming an object that already exists updates it rather than failing.',
        ],
      },
      {
        heading: 'Where it lands',
        body: [
          'A manifest that names its own namespace goes there. One that does not goes to the namespace the tab is on, so it is worth checking which that is before you send it.',
        ],
      },
    ],
  },

  compare: {
    title: 'Compare',
    lede: 'Shows two objects side by side, field by field.',
    sections: [
      {
        heading: 'What it compares',
        body: [
          'The objects as the cluster holds them, including the fields controllers filled in. Two workloads that were created from the same manifest can still differ here, and where they differ is usually the answer.',
        ],
      },
      {
        heading: 'Across clusters',
        body: [
          'Comparing the same workload in two clusters is what this is for — staging against production, one region against another. Fields that identify the object itself will differ; the ones to read are the rest.',
        ],
      },
    ],
  },

  organise: {
    title: 'Organising clusters',
    lede: "Groups, production marks and read-only, all of them PodSteer's own record rather than the cluster's.",
    sections: [
      {
        heading: 'Groups',
        body: [
          'A group is a name you give a set of contexts — a team, an environment, a region. It changes how the list is arranged and nothing about the clusters themselves.',
        ],
      },
      {
        heading: 'Marked production',
        body: [
          'Marking a group production turns on the gates: a banner on every destructive dialog, and a type-the-name confirmation before a delete or a scale to zero.',
          'It is a label you apply, not something PodSteer detects. Nothing about a cluster tells it which one somebody would rather not break.',
        ],
      },
      {
        heading: 'Read-only',
        body: [
          'Read-only makes PodSteer refuse its OWN writes to a cluster — every action, at the boundary, before anything reaches the API server. It does not change your RBAC, and it does not police a shell you open yourself with your own credentials.',
        ],
      },
    ],
  },

  'service-ports': {
    title: 'Service ports',
    lede: 'What a Service listens on, what it forwards to, and how PodSteer forwards a port onto one.',
    sections: [
      {
        heading: 'Kubernetes does not forward to a Service',
        body: [
          'The API server forwards to a pod, and only to a pod. Forwarding to a Service is done on this side: PodSteer reads the Service, finds a ready pod behind it, translates the service port to that pod\u2019s container port, and forwards to the pod. kubectl does the same thing when you type service/name.',
          'One thing here is not kubectl\u2019s behaviour. The forward keeps the Service\u2019s selector, so when the pod it landed on goes away PodSteer finds another pod behind the same Service and rebinds the same local port. kubectl drops the forward, and whatever was pointed at it with it.',
        ],
      },
      {
        heading: 'Port, target port, node port',
        body: [
          'The port is what the Service listens on. The target port is what it forwards to on the pod \u2014 a number, or a name that only means something against a particular pod\u2019s containers, which is why a named one is left as a name here and resolved when the forward is made. A target port that is not set at all defaults to the service port.',
          'A node port, where there is one, is the port that Service is published on across every node in the cluster.',
        ],
      },
      {
        heading: 'When there is no Forward button',
        body: [
          'Three cases, and each one says which it is on the row. An ExternalName Service is a DNS alias with no pod behind it. A Service with no selector has endpoints somebody manages by hand, so there is no pod PodSteer can find. And port-forward carries TCP only \u2014 a UDP forward would appear to establish and then drop every packet, which is worse than being told no.',
        ],
      },
      {
        heading: 'The local port',
        body: [
          'Left empty, the operating system chooses one and the address you get says which. Whatever ends up bound is remembered against that port\u2019s name, so the next time you forward it the field is already filled in.',
        ],
      },
    ],
  },

  'object-data': {
    title: 'Secret and ConfigMap keys',
    lede: 'The keys an object holds, edited one at a time rather than through its YAML.',
    sections: [
      {
        heading: 'A Secret is masked until you ask',
        body: [
          'Values never arrive in the clear: the adapter replaces each one with its byte count before it leaves the backend, so what the panel holds is <hidden, 40 bytes> and not the key. Revealing one value is a separate request for that one key, and it is recorded in the log with the cluster, namespace, object and key — never the value.',
          'Editing appears only after a value is on screen. Writing over something nobody has looked at is the mistake that ordering exists to prevent.',
        ],
      },
      {
        heading: 'A ConfigMap is not a Secret',
        body: [
          'Its values are already in the manifest in the clear, so there is nothing to reveal and the editor is offered straight away. That difference is deliberate: treating a ConfigMap as secret would teach people that masking means something when it does not.',
        ],
      },
      {
        heading: 'What is written',
        body: [
          'One key. The rest of the object is untouched, which is what makes this different from editing the YAML: no resourceVersion race with whoever else is holding the object, and no chance of pasting a whole document over a field you did not mean to change.',
          'Binary data is listed by size and has no editor. A text box over base64 is how a keystore acquires a stray newline.',
        ],
      },
    ],
  },

  reachability: {
    title: 'Reachability',
    lede: 'One probe, when you ask for one, and a separate answer for each thing that can go wrong.',
    sections: [
      {
        heading: 'Two vantages, two different questions',
        body: [
          'From this machine means through the API server named in your kubeconfig — its service proxy, or a port-forward tunnelled through it. A success says the API server reached the endpoints, which is not the same as another workload in the cluster being able to.',
          'From inside the cluster asks a container you name to try, over the same exec session a file copy uses. That is the answer that includes NetworkPolicy, the mesh, and DNS as that pod sees it. Nothing is created for it: no pod, no sidecar, no file written in anybody\u2019s container.',
        ],
      },
      {
        heading: 'Why resolution and connection are separate',
        body: [
          'A name that does not resolve and an address that refuses a connection need opposite next steps \u2014 one is a Service that is not there or a resolver not serving it, the other is a policy, a listener that is not up, or a port nothing binds. One red cross saying \u201cunreachable\u201d sends people to the wrong half of their cluster.',
        ],
      },
      {
        heading: 'The certificate is its own step',
        body: [
          'A service can answer on 443 with a certificate that expired last week: reachable and unusable at the same time, because every client that verifies refuses it. So the probe asks separately, and the HTTP request beside it deliberately does not verify \u2014 otherwise a self-signed endpoint would report nothing at all about what it serves.',
          'It is verified against the CA bundle in the container doing the probing, which is the honest scope: a certificate signed by a private CA your application trusts, from a container that does not, reports as untrusted here and works fine in production. The reason shown is curl\u2019s own words.',
          'Probing from this machine says nothing about a certificate, and says so rather than leaving the row out. The API server does not verify what it proxies, and a forward carries the connection to 127.0.0.1, which no certificate can name. To read the certificate itself \u2014 issuer, expiry, the hosts it covers \u2014 open the Secret that holds it.',
        ],
      },
      {
        heading: 'What it costs',
        body: [
          'Nothing runs on the refresh tick. Each attempt is bounded by a few seconds, and the in-cluster probe uses the tools already in the image \u2014 a container with no nc, curl or wget says so instead of reporting a failure that would be about the image rather than about your Service.',
        ],
      },
    ],
  },

  'saved-views': {
    title: 'Saved views',
    lede: 'A question you ask often, kept under a name.',
    sections: [
      {
        heading: 'What a view holds',
        body: [
          'The kind, the namespace, the search and the status chips \u2014 the four controls in this row. Applying one sets all four at once and reloads the list.',
          'It does not hold the sort, the page, the column set or the page size. Each of those is already remembered on its own, per kind or for the whole application, and a view that set them too would silently change every other visit to that kind.',
        ],
      },
      {
        heading: 'Saving over one',
        body: [
          'A name already in use replaces that view and keeps its place in the list. Saving \u201cCrashing pods\u201d twice is correcting it, not collecting two of them.',
          'The field is pre-filled with the name of the view you are looking at, if one matches, so adjusting a filter and pressing Save updates the view you were plainly working on.',
        ],
      },
      {
        heading: 'They are not tied to a cluster',
        body: [
          'The same views are offered on every cluster tab, because a view is a shape of question rather than a fact about one cluster. A namespace that does not exist here simply shows nothing, the same answer the namespace picker gives.',
        ],
      },
      {
        heading: 'Where they are kept',
        body: [
                    'On this machine, in the interface’s own storage, alongside the other things you have chosen. Not in the settings export: a view holds a namespace and the text you typed, and that file promises no object names appear in it, because people keep it in git and send it to colleagues.',
        ],
      },
    ],
  },

  'node-capacity': {
    title: 'A node\u2019s three numbers',
    lede: 'Allocatable, requested and used are three different measurements, and everything interesting is in the gaps between them.',
    sections: [
      {
        heading: 'What each one is',
        body: [
          'Allocatable is what the kubelet says the scheduler may hand out \u2014 the machine\u2019s capacity less what is held back for the kubelet and the system.',
          'Requested is what the pods already placed here reserved. It is the ONLY one the scheduler looks at, so it decides whether anything new can land, however idle the machine is.',
          'Used is what metrics-server measured. It decides whether the machine is actually working, and whether the kubelet is about to start evicting.',
        ],
      },
      {
        heading: 'The gap that explains a stuck cluster',
        body: [
          'A node can be 95% reserved and 8% used at the same time. Nothing new will schedule onto it and every graph looks calm \u2014 which is why the three are on one surface here rather than one figure on this panel and another on the overview.',
          'The opposite happens too: pods that request nothing at all leave the scheduler counting a busy node as free. Both are said in words under the chart, and only when the numbers disagree in a way you would act on.',
        ],
      },
      {
        heading: 'Where the figures come from',
        body: [
          'Allocatable is the node object\u2019s own. Used comes from metrics-server, and is absent rather than zero when nothing answered. Requested is summed from the pods on this node by the cluster assessment, which runs on every poll whatever view is open \u2014 so it is the same number the overview\u2019s per-node load grid shows, not a second calculation of it.',
          'Requests are a floor rather than a cap: a pod may use more than it reserved unless a limit stops it. A node working past its requests is not a fault, and the note here says so.',
        ],
      },
    ],
  },

  'resize-container': {
    title: 'Resize a running container',
    lede: 'Change one container\u2019s CPU and memory without recreating the pod.',
    sections: [
      {
        heading: 'What in-place means',
        body: [
          'A pod\u2019s containers are otherwise immutable: changing a workload\u2019s resources rolls it, replacing every pod. This writes to the pod\u2019s own resize subresource instead, and the kubelet changes the running container\u2019s cgroup \u2014 the process keeps its memory, its connections and its uptime.',
          'It needs Kubernetes 1.33 or newer. An older cluster says so rather than reporting a missing pod.',
        ],
      },
      {
        heading: 'Whether it restarts the container',
        body: [
          'That is decided by the container\u2019s own resizePolicy, one entry per resource. The default is NotRequired, which is the live change described above. A container whose author chose RestartContainer for a resource gets killed and restarted when you change that resource \u2014 this dialog reads the policy and says which is about to happen before you press anything.',
          'Only the resource you actually change matters: a container that restarts for memory is not restarted by a CPU change.',
        ],
      },
      {
        heading: 'An empty box means leave it alone',
        body: [
          'Only the figures you type are sent. That is not the same as clearing one \u2014 there is no way to remove a request from here, because a request removed and a request set to zero are different things and neither is what an empty box means.',
          'A limit below its request is refused before anything is sent, as is a change that changes nothing: writing the figure that is already set still records a change on the object.',
        ],
      },
      {
        heading: 'What happens next is the kubelet\u2019s answer',
        body: [
          'PodSteer does not claim the resize was applied. The kubelet may apply it immediately, defer it until the node has room, or call it infeasible \u2014 and it says which as a condition on the pod, which appears in this pod\u2019s findings. Deferred means it may still happen; infeasible means this node never can, and the request has to change or the pod has to move.',
        ],
      },
    ],
  },

  settings: {
    title: 'Settings',
    lede: 'How PodSteer behaves on this machine. None of it is stored on a cluster.',
    sections: [
      {
        heading: 'Where it is kept',
        body: [
          'Settings live in a file in your own configuration directory. There is no account, nothing is synchronised anywhere, and nothing about your clusters leaves this machine.',
        ],
      },
      {
        heading: 'Refresh',
        body: [
          'How often the open view re-reads the cluster. Every view reads only what it is showing, so a shorter interval costs API calls in proportion to what you have open, not to the size of the cluster.',
        ],
      },
    ],
  },
} satisfies Record<string, HelpTopic>

/** The ids that exist. A dialog naming anything else does not compile. */
export type HelpTopicId = keyof typeof HELP_TOPICS

/** The topic for an id, or null — used by the panel, which takes a string. */
export function helpTopic(id: string | null): HelpTopic | null {
  if (id === null) return null
  return (HELP_TOPICS as Record<string, HelpTopic>)[id] ?? null
}
