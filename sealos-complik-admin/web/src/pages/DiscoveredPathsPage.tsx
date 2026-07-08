import { ExternalLink, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import {
  Button,
  ConfirmModal,
  DetailList,
  EmptyState,
  Field,
  Input,
  Modal,
  PageHeader,
  SurfaceCard,
} from "../components/ui";
import {
  deleteDiscoveredPathRecord,
  listDiscoveredPaths,
  listDiscoveredRoutes,
} from "../lib/api";
import type {
  CursorPaginatedRecords,
  DiscoveredListQuery,
  DiscoveredPathRecord,
  DiscoveredRouteRecord,
} from "../types";

const pageLimit = 50;
const numberFormatter = new Intl.NumberFormat("zh-CN");

const emptyRoutes: CursorPaginatedRecords<DiscoveredRouteRecord> = {
  list: [],
  hasMore: false,
};

const emptyPaths: CursorPaginatedRecords<DiscoveredPathRecord> = {
  list: [],
  hasMore: false,
};

export function DiscoveredPathsPage() {
  const [namespaceInput, setNamespaceInput] = useState("");
  const [routeQuery, setRouteQuery] = useState<DiscoveredListQuery>({ limit: pageLimit });
  const [routeCursor, setRouteCursor] = useState("");
  const [routeCursorStack, setRouteCursorStack] = useState<string[]>([]);
  const [routePage, setRoutePage] = useState(1);
  const [routesData, setRoutesData] = useState<CursorPaginatedRecords<DiscoveredRouteRecord>>(emptyRoutes);
  const [routesError, setRoutesError] = useState<string | null>(null);
  const [isRoutesLoading, setIsRoutesLoading] = useState(false);
  const [selectedRoute, setSelectedRoute] = useState<DiscoveredRouteRecord | null>(null);
  const [pathsData, setPathsData] = useState<CursorPaginatedRecords<DiscoveredPathRecord>>(emptyPaths);
  const [pathCursor, setPathCursor] = useState("");
  const [pathCursorStack, setPathCursorStack] = useState<string[]>([]);
  const [pathPage, setPathPage] = useState(1);
  const [pathsError, setPathsError] = useState<string | null>(null);
  const [isPathsLoading, setIsPathsLoading] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<DiscoveredPathRecord | null>(null);

  const routeRows = routesData.list;
  const pathRows = pathsData.list;

  const selectedRouteQuery = useMemo<DiscoveredListQuery | null>(() => {
    if (!selectedRoute) {
      return null;
    }

    return {
      limit: pageLimit,
      namespace: selectedRoute.namespace,
      ingressName: selectedRoute.ingressName,
      host: selectedRoute.host,
    };
  }, [selectedRoute]);

  const resetRouteCursor = () => {
    setRouteCursor("");
    setRouteCursorStack([]);
    setRoutePage(1);
  };

  const resetPathCursor = () => {
    setPathCursor("");
    setPathCursorStack([]);
    setPathPage(1);
  };

  const loadRoutes = useCallback(async () => {
    setIsRoutesLoading(true);
    setRoutesError(null);
    try {
      const nextData = await listDiscoveredRoutes({
        ...routeQuery,
        cursor: routeCursor || undefined,
        limit: pageLimit,
      });
      setRoutesData(nextData);
    } catch (err) {
      setRoutesError(err instanceof Error ? err.message : "入口列表加载失败");
      setRoutesData(emptyRoutes);
    } finally {
      setIsRoutesLoading(false);
    }
  }, [routeCursor, routeQuery]);

  const loadPaths = useCallback(async () => {
    if (!selectedRouteQuery) {
      setPathsData(emptyPaths);
      return;
    }

    setIsPathsLoading(true);
    setPathsError(null);
    try {
      const nextData = await listDiscoveredPaths({
        ...selectedRouteQuery,
        cursor: pathCursor || undefined,
      });
      setPathsData(nextData);
    } catch (err) {
      setPathsError(err instanceof Error ? err.message : "路径明细加载失败");
      setPathsData(emptyPaths);
    } finally {
      setIsPathsLoading(false);
    }
  }, [pathCursor, selectedRouteQuery]);

  useEffect(() => {
    void loadRoutes();
  }, [loadRoutes]);

  useEffect(() => {
    resetPathCursor();
    setPathsError(null);
    setPathsData(emptyPaths);
  }, [selectedRoute]);

  useEffect(() => {
    void loadPaths();
  }, [loadPaths]);

  function handleSearchSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    resetRouteCursor();
    setRouteQuery({
      limit: pageLimit,
      namespace: namespaceInput.trim(),
    });
  }

  function goNextRoutePage() {
    if (!routesData.nextCursor) {
      return;
    }

    setRouteCursorStack((current) => [...current, routeCursor]);
    setRouteCursor(routesData.nextCursor);
    setRoutePage((current) => current + 1);
  }

  function goPreviousRoutePage() {
    setRouteCursorStack((current) => {
      if (current.length === 0) {
        return current;
      }

      const nextStack = current.slice(0, -1);
      setRouteCursor(current[current.length - 1] ?? "");
      setRoutePage((page) => Math.max(1, page - 1));
      return nextStack;
    });
  }

  function goNextPathPage() {
    if (!pathsData.nextCursor) {
      return;
    }

    setPathCursorStack((current) => [...current, pathCursor]);
    setPathCursor(pathsData.nextCursor);
    setPathPage((current) => current + 1);
  }

  function goPreviousPathPage() {
    setPathCursorStack((current) => {
      if (current.length === 0) {
        return current;
      }

      const nextStack = current.slice(0, -1);
      setPathCursor(current[current.length - 1] ?? "");
      setPathPage((page) => Math.max(1, page - 1));
      return nextStack;
    });
  }

  function openRoute(route: DiscoveredRouteRecord) {
    setSelectedRoute(route);
    resetPathCursor();
  }

  return (
    <div className="page-container">
      <PageHeader
        kicker="Ingress Paths"
        title="入口路径"
        description="按 namespace 查看网关日志发现的真实访问 path。"
        actions={
          <Button variant="secondary" onClick={() => void loadRoutes()}>
            <RefreshCw size={16} /> 刷新
          </Button>
        }
      />

      <SurfaceCard>
        <form className="toolbar" onSubmit={handleSearchSubmit}>
          <Field label="namespace">
            <div className="search-control">
              <Input
                placeholder="输入 namespace"
                value={namespaceInput}
                onChange={(event) => setNamespaceInput(event.target.value)}
              />
              <Button variant="secondary" type="submit">
                <Search size={16} /> 搜索
              </Button>
            </div>
          </Field>
        </form>
      </SurfaceCard>

      <SurfaceCard className="data-table-wrap" padded={false}>
        {routesError ? (
          <div style={{ padding: 20 }}>
            <EmptyState
              title="入口列表加载失败"
              description={routesError}
              action={<Button variant="secondary" onClick={() => void loadRoutes()}>重新加载</Button>}
            />
          </div>
        ) : isRoutesLoading ? (
          <div style={{ padding: 20 }}>
            <EmptyState title="入口列表加载中" description="正在同步网关日志发现的入口聚合数据。" />
          </div>
        ) : routeRows.length > 0 ? (
          <table className="data-table discovered-routes-table">
            <thead>
              <tr>
                <th>namespace</th>
                <th>ingress</th>
                <th>host</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {routeRows.map((item) => (
                <tr key={item.id}>
                  <td>
                    <button className="namespace-link table-row-button" onClick={() => openRoute(item)} type="button">
                      {item.namespace}
                    </button>
                  </td>
                  <td>
                    <button className="table-row-button" onClick={() => openRoute(item)} type="button">
                      <strong>{item.ingressName}</strong>
                    </button>
                  </td>
                  <td>{item.host}</td>
                  <td>
                    <Button variant="ghost" onClick={() => openRoute(item)}>
                      查看路径
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div style={{ padding: 20 }}>
            <EmptyState title="等待入口路径数据" description="VLogPathDiscovery 写入数据后会在这里展示入口聚合列表。" />
          </div>
        )}
      </SurfaceCard>

      <CursorPager
        hasNext={routesData.hasMore}
        hasPrevious={routeCursorStack.length > 0}
        label={`第 ${routePage} 页，每页 ${pageLimit} 条`}
        onNext={goNextRoutePage}
        onPrevious={goPreviousRoutePage}
      />

      <Modal
        className="path-modal-card"
        description="这里展示该入口下由网关访问日志发现的 path 明细。"
        onClose={() => setSelectedRoute(null)}
        open={Boolean(selectedRoute)}
        title={selectedRoute ? `${selectedRoute.namespace} / ${selectedRoute.ingressName}` : ""}
      >
        {selectedRoute ? (
          <div className="panel-stack">
            <DetailList
              items={[
                { label: "namespace", value: selectedRoute.namespace },
                { label: "ingress", value: selectedRoute.ingressName },
                { label: "host", value: selectedRoute.host },
              ]}
            />

            <div>
              <div className="section-title-row">
                <div>
                  <h3 className="section-title">Path 明细</h3>
                  <p className="section-subtitle">按访问次数由高到低展示，单条 path 可手动删除。</p>
                </div>
                <Button variant="secondary" onClick={() => void loadPaths()}>
                  <RefreshCw size={16} /> 刷新
                </Button>
              </div>

              <div className="data-table-wrap">
                {pathsError ? (
                  <div style={{ padding: 20 }}>
                    <EmptyState
                      title="路径明细加载失败"
                      description={pathsError}
                      action={<Button variant="secondary" onClick={() => void loadPaths()}>重新加载</Button>}
                    />
                  </div>
                ) : isPathsLoading ? (
                  <div style={{ padding: 20 }}>
                    <EmptyState title="路径明细加载中" description="正在同步当前入口的 path 明细。" />
                  </div>
                ) : pathRows.length > 0 ? (
                  <table className="data-table discovered-paths-table">
                    <thead>
                      <tr>
                        <th>URL</th>
                        <th>访问次数</th>
                        <th>最近访问</th>
                        <th>最近检测</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pathRows.map((item) => (
                        <tr key={item.id}>
                          <td>
                            <a
                              className="path-cell path-link"
                              href={buildDiscoveredPathURL(item.host, item.path)}
                              rel="noreferrer"
                              target="_blank"
                            >
                              <span>{buildDiscoveredPathURL(item.host, item.path)}</span>
                              <ExternalLink size={14} />
                            </a>
                          </td>
                          <td>{numberFormatter.format(item.count)}</td>
                          <td>{item.lastSeenAt}</td>
                          <td>{item.lastDetectedAt ?? "-"}</td>
                          <td>
                            <Button variant="danger" onClick={() => setPendingDelete(item)}>
                              删除
                            </Button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : (
                  <div style={{ padding: 20 }}>
                    <EmptyState title="等待 path 明细" description="该入口的真实访问 path 会在 VLog 写入后展示。" />
                  </div>
                )}
              </div>

              <CursorPager
                hasNext={pathsData.hasMore}
                hasPrevious={pathCursorStack.length > 0}
                label={`第 ${pathPage} 页，每页 ${pageLimit} 条`}
                onNext={goNextPathPage}
                onPrevious={goPreviousPathPage}
              />
            </div>
          </div>
        ) : null}
      </Modal>

      <ConfirmModal
        description={pendingDelete ? `删除 path ${pendingDelete.path} 后，入口聚合数量会随下一次刷新更新。` : ""}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => {
          if (!pendingDelete) {
            return;
          }

          void deleteDiscoveredPathRecord(pendingDelete.apiId).then(() => {
            setPendingDelete(null);
            void loadPaths();
            void loadRoutes();
          });
        }}
        open={Boolean(pendingDelete)}
        title="删除发现路径"
      />
    </div>
  );
}

function CursorPager({
  hasNext,
  hasPrevious,
  label,
  onNext,
  onPrevious,
}: {
  hasNext: boolean;
  hasPrevious: boolean;
  label: string;
  onNext: () => void;
  onPrevious: () => void;
}) {
  return (
    <div className="pagination-row">
      <span className="muted-text">{label}</span>
      <div className="button-row">
        <Button variant="secondary" disabled={!hasPrevious} onClick={onPrevious}>
          上一页
        </Button>
        <Button variant="secondary" disabled={!hasNext} onClick={onNext}>
          下一页
        </Button>
      </div>
    </div>
  );
}

function buildDiscoveredPathURL(host: string, path: string) {
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  const base = /^https?:\/\//i.test(host) ? host : `https://${host}`;

  try {
    return new URL(normalizedPath, base).toString();
  } catch {
    return `${base}${normalizedPath}`;
  }
}
