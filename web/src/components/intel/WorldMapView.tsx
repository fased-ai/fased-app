import { useUI } from "../../ui-preferences";
import {
  INTEL_REGIONS,
  projectLatLon,
  type CorrelatedCluster,
  type IntelEvent,
} from "../../world-intel";

const MAP_WIDTH = 960;
const MAP_HEIGHT = 460;

// Simplified clean-room equirectangular landmass polygons aligned with projectLatLon().
const LANDMASS_POLYGONS = [
  // North America
  "120,42 260,36 330,68 308,118 270,152 246,192 212,216 175,180 140,124 108,82",
  // Greenland
  "342,16 408,14 416,52 374,70 338,48",
  // South America
  "262,232 326,228 366,274 352,340 310,410 282,402 276,320 250,268",
  // Europe
  "448,66 534,54 574,90 562,132 518,144 470,136 446,110",
  // Africa
  "450,156 532,150 578,196 570,270 536,348 498,352 478,282 442,222",
  // Asia / Middle East
  "566,58 760,44 854,78 830,148 776,204 714,228 656,216 616,184 562,166 568,120",
  // Australia / Oceania
  "746,294 824,292 850,342 820,378 758,366 734,332",
];

export function WorldMapView({
  events,
  clusters,
  regionCounts,
  selectedRegion,
  selectedEventId,
  onSelectRegion,
  onSelectEvent,
}: {
  events: IntelEvent[];
  clusters: CorrelatedCluster[];
  regionCounts: Record<string, number>;
  selectedRegion: string;
  selectedEventId: string;
  onSelectRegion: (region: string) => void;
  onSelectEvent: (ev: IntelEvent) => void;
}) {
  const { t } = useUI();
  const totalCount = events.length;

  return (
    <div className="intel-map-card">
      <div
        className="intel-region-bar"
        role="group"
        aria-label={t("World map")}
      >
        {INTEL_REGIONS.map((reg) => {
          const count = reg.id ? (regionCounts[reg.id] ?? 0) : totalCount;
          const active = selectedRegion === reg.id;
          return (
            <button
              key={reg.id || "all"}
              type="button"
              className={`intel-filter-pill ${active ? "is-selected" : ""}`}
              aria-pressed={active}
              onClick={() => onSelectRegion(reg.id)}
            >
              <span>{t(reg.label)}</span>
              <span className="intel-pill-count">{count}</span>
            </button>
          );
        })}
      </div>

      <div
        className="intel-map-stage"
        role="region"
        aria-label={t("World map")}
      >
        <svg
          className="intel-map-svg"
          viewBox={`0 0 ${MAP_WIDTH} ${MAP_HEIGHT}`}
          role="img"
          aria-label={t("World map")}
        >
          <rect
            x={0}
            y={0}
            width={MAP_WIDTH}
            height={MAP_HEIGHT}
            className="intel-map-ocean"
          />
          {[-30, 0, 30, 60].map((lat) => {
            const { y } = projectLatLon(lat, 0, MAP_WIDTH, MAP_HEIGHT);
            return (
              <line
                key={`lat-${lat}`}
                x1={0}
                y1={y}
                x2={MAP_WIDTH}
                y2={y}
                className="intel-map-grid"
              />
            );
          })}
          {[-120, -60, 0, 60, 120].map((lon) => {
            const { x } = projectLatLon(0, lon, MAP_WIDTH, MAP_HEIGHT);
            return (
              <line
                key={`lon-${lon}`}
                x1={x}
                y1={0}
                x2={x}
                y2={MAP_HEIGHT}
                className="intel-map-grid"
              />
            );
          })}
          {LANDMASS_POLYGONS.map((pts, idx) => (
            <polygon key={idx} points={pts} className="intel-map-land" />
          ))}
          {clusters.map((cl) => {
            const { x, y } = projectLatLon(
              cl.lat,
              cl.lon,
              MAP_WIDTH,
              MAP_HEIGHT,
            );
            return (
              <circle
                key={`cl-${cl.id}`}
                cx={x}
                cy={y}
                r={Math.min(26, 10 + cl.eventCount * 3)}
                className="intel-map-cluster"
              />
            );
          })}
          {events.slice(0, 60).map((ev) => {
            const { x, y } = projectLatLon(
              ev.lat,
              ev.lon,
              MAP_WIDTH,
              MAP_HEIGHT,
            );
            const selected = ev.id === selectedEventId;
            const severity =
              ev.urgency >= 0.7 ? "high" : ev.urgency >= 0.45 ? "mid" : "low";
            return (
              <g
                key={ev.id}
                transform={`translate(${x}, ${y})`}
                className="intel-map-pin-group"
                data-severity={severity}
                data-selected={selected ? "true" : undefined}
              >
                <circle r={selected ? 9 : 6} className="intel-map-pin" />
              </g>
            );
          })}
        </svg>

        <div className="intel-map-overlay-pins">
          {events.slice(0, 60).map((ev) => {
            const { x, y } = projectLatLon(
              ev.lat,
              ev.lon,
              MAP_WIDTH,
              MAP_HEIGHT,
            );
            const leftPct = (x / MAP_WIDTH) * 100;
            const topPct = (y / MAP_HEIGHT) * 100;
            const selected = ev.id === selectedEventId;
            const severity =
              ev.urgency >= 0.7 ? "high" : ev.urgency >= 0.45 ? "mid" : "low";
            return (
              <button
                key={`btn-${ev.id}`}
                type="button"
                className={`intel-pin-btn ${selected ? "is-selected" : ""}`}
                data-severity={severity}
                data-testid={`map-pin-${ev.id}`}
                style={{ left: `${leftPct}%`, top: `${topPct}%` }}
                aria-label={`${ev.title} (${Math.round(ev.urgency * 100)}%)`}
                aria-pressed={selected}
                onClick={() => onSelectEvent(ev)}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}
