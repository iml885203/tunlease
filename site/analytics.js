(() => {
  const product = 'tunlease';
  if (location.hostname !== product + '.dotw.me' || window.posthog ||
      navigator.doNotTrack === '1' || navigator.globalPrivacyControl === true) return;

  // PostHog's public browser loader queues events until its asynchronous SDK arrives.
  !function(t,e){var o,n,p,r;e.__SV||(window.posthog&&window.posthog.__loaded)||(window.posthog=e,e._i=[],e.init=function(i,s,a){function g(t,e){var o=e.split(".");2==o.length&&(t=t[o[0]],e=o[1]),t[e]=function(){t.push([e].concat(Array.prototype.slice.call(arguments,0)))}}p||((p=t.createElement("script")).type="text/javascript",p.crossOrigin="anonymous",p.async=!0,p.src=s.api_host.replace(".i.posthog.com","-assets.i.posthog.com")+"/static/array.js",p.onerror=function(){p=null},(r=t.getElementsByTagName("script")[0]).parentNode.insertBefore(p,r));var u=e;for(void 0!==a?u=e[a]=[]:a="posthog",u.people=u.people||[],o="capture register register_once identify set_config opt_in_capturing opt_out_capturing".split(" "),n=0;n<o.length;n++)g(u,o[n]);e._i.push([i,s,a])},e.__SV=1)}(document,window.posthog||[]);

  function withoutQuery(value) {
    try {
      const url = new URL(value);
      return url.origin + url.pathname;
    } catch {
      return '';
    }
  }

  posthog.init('phc_ydjwiFUWWyhfSmscH9GgTCK9R429NwFpkjeyii7dwiTD', {
    api_host: 'https://us.i.posthog.com',
    defaults: '2026-05-30',
    person_profiles: 'identified_only',
    autocapture: false,
    capture_pageview: 'history_change',
    capture_pageleave: true,
    disable_session_recording: true,
    disable_surveys: true,
    capture_exceptions: false,
    capture_dead_clicks: false,
    capture_heatmaps: false,
    capture_performance: false,
    rageclick: false,
    before_send(event) {
      if (!['$pageview', '$pageleave', 'project_link_clicked'].includes(event.event)) return null;
      for (const properties of [event.properties, event.$set, event.$set_once, event.properties.$set, event.properties.$set_once]) {
        if (!properties) continue;
        for (const key of Object.keys(properties)) {
          if (typeof properties[key] === 'string' && /^https?:\/\//i.test(properties[key])) {
            properties[key] = withoutQuery(properties[key]);
          }
        }
      }
      event.properties.product = product;
      event.properties.site_domain = location.hostname;
      return event;
    },
  });

  document.addEventListener('click', (event) => {
    const link = event.target instanceof Element ? event.target.closest('a[href]') : null;
    if (!link) return;
    const destination = new URL(link.href, location.href);
    const projects = {
      'about.dotw.me': 'about', 'blog.dotw.me': 'blog',
      'orbit.dotw.me': 'orbit', 'tunlease.dotw.me': 'tunlease',
    };
    const destinationProduct = projects[destination.hostname] ||
      (destination.hostname === 'github.com' && /^\/iml885203\/(orbit|tunlease)(?:\/|$)/.exec(destination.pathname)?.[1]);
    if (!destinationProduct || destination.origin === location.origin) return;
    posthog.capture('project_link_clicked', {
      destination_product: destinationProduct,
      destination_host: destination.hostname,
      destination_path: destination.pathname,
    });
  });
})();
