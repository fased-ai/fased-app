import { Component, type ErrorInfo, type ReactNode } from "react";

type Props = {
  children: ReactNode;
  title?: string;
  reloadLabel?: string;
  retryLabel?: string;
};

type State = {
  hasError: boolean;
  message: string;
};

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, message: "" };

  static getDerivedStateFromError(error: Error): State {
    return {
      hasError: true,
      message: error?.message || "Unexpected interface error",
    };
  }

  componentDidCatch(_error: Error, _info: ErrorInfo) {
    // Keep error presentation bounded inside the UI without dumping sensitive state.
  }

  render() {
    if (!this.state.hasError) {
      return this.props.children;
    }
    return (
      <main className="main-content" role="alert">
        <section className="panel">
          <div className="panel-heading">
            <h2>{this.props.title || "Something went wrong"}</h2>
          </div>
          <p className="agent-record-body">{this.state.message}</p>
          <div className="agent-inline">
            <button
              type="button"
              className="primary"
              onClick={() => window.location.reload()}
            >
              {this.props.reloadLabel || "Reload"}
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => this.setState({ hasError: false, message: "" })}
            >
              {this.props.retryLabel || "Try again"}
            </button>
          </div>
        </section>
      </main>
    );
  }
}
