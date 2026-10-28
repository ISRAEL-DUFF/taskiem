import { Component, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "./ui";

/** Keeps one page's failure to that page: instead of a blank app, it says
 * so in plain words and offers to draw the page again. The console keys
 * each page by its address, so moving to another page starts afresh. */
export class PageBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  componentDidCatch(error: unknown) {
    console.error(error);
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div role="alert" data-testid="page-failed">
        <h1>This page stopped working</h1>
        <EmptyState
          icon="info"
          action={
            <>
              <button type="button" className="primary" onClick={() => this.setState({ failed: false })}>
                Try again
              </button>{" "}
              <Link className="button" to="/home">
                Go to Home
              </Link>
            </>
          }
        >
          Something on this page could not be shown. The rest of Taskiem works; try again, or go to Home.
        </EmptyState>
      </div>
    );
  }
}
