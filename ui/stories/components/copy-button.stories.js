import hbs from 'htmlbars-inline-precompile';
import { withKnobs, text } from '@storybook/addon-knobs';

export default {
  title: 'Components|Copy Button',
  decorators: [withKnobs],
};

export const CopyButton = () => {
  return {
    template: hbs`
      <h5 class="title is-5">Copy Button</h5>
      <span class="tag is-hollow is-small no-text-transform">
        {{clipboardText}}
        <CopyButton @clipboardText={{clipboardText}} />
      </span>
    `,
    context: {
      clipboardText: text('Clipboard Text', 'e8c898a0-794b-9063-7a7f-bf0c4a405f83'),
    },
  };
};
