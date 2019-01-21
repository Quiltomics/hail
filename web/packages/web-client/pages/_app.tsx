import App, { Container } from 'next/app';
import Header from '../components/Header';
import { initialize, isAuthenticated, initStateSSR } from '../libs/auth';
import Router from 'next/router';

// import cookies from '../libs/cookies';

import 'styles/main.scss';
import 'animate.css';
import 'normalize.css';

// TODO: set some kind of protected property on routes, instead of
// blacklisting here
const protectedRoute: any = {
  '/notebook': true
};

// let authInitialized = false;
// TODO: think about using React context to pass down auth state instead of prop
export default class MyApp extends App {
  state = {
    isDark: false
  };

  // This runs:
  // 1) SSR Mode: One time
  // 2) Client mode: After constructor (on client-side route transitions)

  static async getInitialProps({
    Component,
    ctx
  }: {
    Component: any;
    ctx: any;
  }) {
    let pageProps = {};

    if (typeof window === 'undefined') {
      initStateSSR(ctx.req.headers.cookie);
    }

    // ctx.pathname will not include get variables in the query
    // will include the full directory path /path/to/resource
    if (!isAuthenticated() && protectedRoute[ctx.pathname] === true) {
      if (ctx.res) {
        ctx.res.writeHead(303, { Location: '/login?redirect=true' });
        ctx.res.end();
        return { pageProps: null };
      }
      Router.replace('/login?redirect=true');
      return { pageProps: null };
    }

    if (Component.getInitialProps) {
      pageProps = await Component.getInitialProps(ctx);
    }

    return { pageProps };
  }

  onDarkToggle = () => {
    this.setState((prevState: any) => {
      return {
        isDark: !prevState.isDark
      };
    });
  };

  constructor(props: any) {
    super(props);

    // TOOD: For any components that need to fetch during server phase
    // we will need to extract the accessToken
    if (typeof window !== 'undefined') {
      initialize();
    }
  }

  render() {
    const { Component, pageProps } = this.props;

    return (
      <Container>
        <span id="theme-site" className={this.state.isDark ? 'dark' : ''}>
          <Header />
          <span id="main">
            <Component {...pageProps} />
          </span>

          <span id="footer">
            <i
              className="material-icons"
              style={{ cursor: 'pointer' }}
              onClick={this.onDarkToggle}
            >
              wb_sunny
            </i>
          </span>
        </span>
      </Container>
    );
  }
}