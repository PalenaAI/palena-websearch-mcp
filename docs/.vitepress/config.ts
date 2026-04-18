import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Palena',
  description: 'Enterprise-grade web search MCP server — compliance boundaries built in.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,


  head: [
    ['link', { rel: 'icon', type: 'image/png', href: '/favicon.png' }],
    ['meta', { name: 'theme-color', content: '#0ea5a4' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'Palena — Enterprise web search for AI' }],
    ['meta', { property: 'og:description', content: 'An MCP server that searches the open web, strips PII, screens prompt-injection payloads, and returns LLM-ready markdown with a full audit trail.' }],
    ['meta', { property: 'og:image', content: '/og-image.png' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:image', content: '/og-image.png' }],
  ],

  themeConfig: {
    logo: '/palena-icon.png',
    siteTitle: 'Palena',

    nav: [
      { text: 'Guide', link: '/getting-started', activeMatch: '^/(getting-started|concepts|faq)' },
      { text: 'Reference', link: '/tool-reference', activeMatch: '^/(tool-reference|configuration|integrations)' },
      { text: 'Pipeline', link: '/scraping', activeMatch: '^/(scraping|pii-and-compliance|prompt-injection|reranking|provenance)' },
      { text: 'Operate', link: '/deployment', activeMatch: '^/(deployment|observability)' },
      { text: 'GitHub', link: 'https://github.com/PalenaAI/palena-websearch-mcp' },
    ],

    sidebar: [
      {
        text: 'Start Here',
        collapsed: false,
        items: [
          { text: 'Getting Started', link: '/getting-started' },
          { text: 'Concepts', link: '/concepts' },
          { text: 'FAQ', link: '/faq' },
        ],
      },
      {
        text: 'Using the Server',
        collapsed: false,
        items: [
          { text: 'Tool Reference', link: '/tool-reference' },
          { text: 'Integrations', link: '/integrations' },
          { text: 'Configuration', link: '/configuration' },
        ],
      },
      {
        text: 'The Pipeline',
        collapsed: false,
        items: [
          { text: 'Scraping', link: '/scraping' },
          { text: 'PII & Compliance', link: '/pii-and-compliance' },
          { text: 'Prompt-Injection Defense', link: '/prompt-injection' },
          { text: 'Reranking', link: '/reranking' },
          { text: 'Provenance', link: '/provenance' },
        ],
      },
      {
        text: 'Running in Production',
        collapsed: false,
        items: [
          { text: 'Deployment', link: '/deployment' },
          { text: 'Observability', link: '/observability' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/PalenaAI/palena-websearch-mcp' },
    ],

    search: {
      provider: 'local',
    },

    editLink: {
      pattern: 'https://github.com/PalenaAI/palena-websearch-mcp/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    footer: {
      message: 'Released under the Apache License, Version 2.0.',
      copyright: 'Copyright © 2026 bitkaio LLC',
    },

    outline: {
      level: [2, 3],
      label: 'On this page',
    },
  },
})
