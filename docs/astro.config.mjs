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
		}),
	],
});
