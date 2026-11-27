import { ChevronRight } from "../icons";
import {
  formatTaskSchedule,
  statusPillTone,
  type TaskEdge,
  type UniversalTask,
} from "../tasks-v2";
import { useUI } from "../ui-preferences";

export function TaskDAGView({
  tasks,
  edges,
  selectedId,
  onSelect,
}: {
  tasks: UniversalTask[];
  edges: TaskEdge[];
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  const { t } = useUI();
  const taskMap = new Map(tasks.map((item) => [item.id, item]));

  const incoming = new Map<string, number>();
  const adj = new Map<string, TaskEdge[]>();
  for (const edge of edges) {
    incoming.set(edge.childTaskId, (incoming.get(edge.childTaskId) ?? 0) + 1);
    const list = adj.get(edge.parentTaskId) ?? [];
    list.push(edge);
    adj.set(edge.parentTaskId, list);
  }

  const roots = tasks.filter((item) => (incoming.get(item.id) ?? 0) === 0);
  const layers: UniversalTask[][] = [];
  const placed = new Set<string>();
  let frontier = roots;
  while (frontier.length > 0 && layers.length < 6) {
    layers.push(frontier);
    const next: UniversalTask[] = [];
    for (const node of frontier) {
      placed.add(node.id);
      for (const edge of adj.get(node.id) ?? []) {
        const child = taskMap.get(edge.childTaskId);
        if (
          child &&
          !placed.has(child.id) &&
          !next.some((n) => n.id === child.id)
        ) {
          next.push(child);
        }
      }
    }
    frontier = next;
  }

  const isolated = tasks.filter((item) => !placed.has(item.id));
  if (isolated.length > 0) {
    layers.push(isolated);
  }

  if (tasks.length === 0) {
    return (
      <div className="wt-tasks-empty">
        {t("No task graphs yet. Create a Swarm DAG or instantiate a template.")}
      </div>
    );
  }

  return (
    <div className="wt-dag-canvas" aria-label={t("Task DAG Topology")}>
      <div className="wt-dag-columns">
        {layers.map((layer, depth) => (
          <div key={depth} className="wt-dag-column">
            <div className="wt-dag-stage-label">
              {depth === 0 ? t("Roots") : `${t("Stage")} ${depth}`}
            </div>
            <div className="wt-dag-nodes">
              {layer.map((node) => {
                const parentEdges = edges.filter(
                  (e) => e.childTaskId === node.id,
                );
                const selected = node.id === selectedId;
                return (
                  <button
                    key={node.id}
                    type="button"
                    className={`wt-dag-node ${selected ? "is-selected" : ""}`}
                    onClick={() => onSelect(node.id)}
                  >
                    <div className="wt-dag-node-top">
                      <strong className="wt-dag-node-title">{node.name}</strong>
                      <span className={`f-pill ${statusPillTone(node.status)}`}>
                        {node.status}
                      </span>
                    </div>
                    <div className="wt-dag-node-meta f-mono">
                      <span>{node.goalDomain}</span>
                      <span>{node.execMode}</span>
                      <span>{formatTaskSchedule(node)}</span>
                    </div>
                    {parentEdges.length > 0 && (
                      <div className="wt-dag-edge-tags">
                        {parentEdges.map((edge) => (
                          <span key={edge.id} className="wt-dag-edge-tag">
                            <ChevronRight size={11} aria-hidden="true" />
                            <span>
                              {taskMap.get(edge.parentTaskId)?.name ??
                                edge.parentTaskId}
                            </span>
                            <code>{edge.condition}</code>
                          </span>
                        ))}
                      </div>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
