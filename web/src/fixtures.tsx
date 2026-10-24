import {
  ArrowUpRight,
  Bot,
  Clock3,
  LayoutDashboard,
  TerminalSquare,
  Wallet,
  Zap,
} from "./icons";
export const navigation = [
  { path: "overview", title: "Overview", icon: LayoutDashboard },
  { path: "agents/chat", title: "Agents", icon: Bot },
  { path: "terminals", title: "Terminals", icon: TerminalSquare },
  { path: "wallets/overview", title: "Wallets", icon: Wallet },
  { path: "activity", title: "Activity", icon: Clock3 },
];
export const terminalData = [
  {
    id: "satcoin",
    name: "WEN Satcoin Mining",
    category: "WEN",
    desc: "Preview BTC ranges, bounded SOL sessions, results and claims.",
    icon: Zap,
    color: "lime",
  },
  {
    id: "hyperliquid",
    name: "Hyperliquid",
    category: "TRADING",
    desc: "Positions, order entry and strategy execution.",
    icon: TrendingIcon,
    color: "blue",
  },
  {
    id: "jupiter",
    name: "Solana / Jupiter",
    category: "TRADING",
    desc: "Swaps, watchlists and supported limit orders.",
    icon: ArrowUpRight,
    color: "purple",
  },
];
function TrendingIcon({ size = 20 }: { size?: number }) {
  return <ArrowUpRight size={size} />;
}
