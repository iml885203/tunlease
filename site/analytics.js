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
    autocapture: {dom_event_allowlist: ['click'], element_allowlist: ['a', 'button'], capture_copied_text: false},
    mask_all_text: true,
    mask_all_element_attributes: true,
    disable_capture_url_hashes: true,
    capture_pageview: 'history_change',
    capture_pageleave: true,
    disable_session_recording: false,
    enable_recording_console_log: false,
    session_recording: {
      sampleRate: 1,
      maskAllInputs: true,
      maskTextSelector: '[contenteditable], [data-private], .ph-mask',
      blockSelector: 'input[type="file"], [data-private], .ph-no-capture',
      recordCrossOriginIframes: false,
      recordBody: false,
      recordHeaders: false,
      maskCapturedNetworkRequestFn(request) {
        return request.isInitial ? {...request, name: withoutQuery(request.name)} : null;
      },
      maskAttributeFn(name, value) {
        if (name === 'value' || name.startsWith('data-')) return '***';
        if (['href', 'src', 'action'].includes(name)) return withoutQuery(new URL(value, location.href).href);
        return value;
      },
    },
    disable_surveys: true,
    capture_exceptions: false,
    capture_dead_clicks: false,
    capture_heatmaps: true,
    capture_performance: {web_vitals: true, network_timing: false},
    rageclick: false,
    before_send(event) {
      if (!['$pageview', '$pageleave', 'project_link_clicked', '$autocapture', '$web_vitals', '$$heatmap', '$snapshot'].includes(event.event)) return null;
      function redact(value) {
        if (typeof value === 'string') return /^https?:\/\//i.test(value) ? withoutQuery(value) : value;
        if (Array.isArray(value)) return value.map(redact);
        if (Object.prototype.toString.call(value) === '[object Date]') return value;
        if (value && typeof value === 'object') {
          return Object.fromEntries(Object.entries(value).map(([key, item]) => [
            /^https?:\/\//i.test(key) ? withoutQuery(key) : key, redact(item),
          ]));
        }
        return value;
      }
      event = redact(event);
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
