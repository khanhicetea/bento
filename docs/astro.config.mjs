// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://starlight.astro.build/reference/configuration/
export default defineConfig({
	integrations: [
		starlight({
			title: 'Bento',
			description: 'Documentation for the Bento self-hosted operations layer.',
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
				{ label: 'Core concepts', items: [{ autogenerate: { directory: 'concepts' } }] },
				{ label: 'Stack management', items: [{ autogenerate: { directory: 'guides/stacks' } }] },
				{ label: 'Applications and traffic', items: [{ autogenerate: { directory: 'guides/apps' } }] },
				{ label: 'Data', items: [{ autogenerate: { directory: 'guides/data' } }] },
				{ label: 'Customization', items: [{ autogenerate: { directory: 'guides/customization' } }] },
				{ label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
				{ label: 'Advanced', items: [{ autogenerate: { directory: 'advanced' } }] },
			],
		}),
	],
});
