// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://starlight.astro.build/reference/configuration/
export default defineConfig({
	integrations: [
		starlight({
			title: 'Bento',
			description: 'Documentation for the Bento single-host control plane.',
			social: [
				{
					icon: 'github',
					label: 'Bento on GitHub',
					href: 'https://github.com/khanhicetea/bento',
				},
			],
			editLink: {
				baseUrl: 'https://github.com/khanhicetea/bento/edit/main/docs/',
			},
			sidebar: [
				{ label: 'Home', link: '/' },
				{ label: 'Start here', items: [{ autogenerate: { directory: 'start' } }] },
				{ label: 'Concepts', items: [{ autogenerate: { directory: 'concepts' } }] },
				{ label: 'Applications', items: [{ autogenerate: { directory: 'guides/apps' } }] },
				{ label: 'Ingress', items: [{ autogenerate: { directory: 'guides/ingress' } }] },
				{ label: 'Data', items: [{ autogenerate: { directory: 'guides/data' } }] },
				{ label: 'Stacks', items: [{ autogenerate: { directory: 'guides/stacks' } }] },
				{ label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
				{ label: 'Advanced', items: [{ autogenerate: { directory: 'advanced' } }] },
			],
		}),
	],
});
