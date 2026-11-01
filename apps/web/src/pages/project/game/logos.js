// Each server type's own logo: the project's GitHub organisation avatar, and Minecraft's icon from
// minecraft.net for Vanilla and Bedrock. Bundled, so setup never loads images from third parties.
import paper from './logos/paper.png';
import fabric from './logos/fabric.png';
import neoforge from './logos/neoforge.png';
import forge from './logos/forge.jpg';
import mohist from './logos/mohist.png';
import minecraft from './logos/minecraft.png';
import modpack from './logos/modpack.jpg';
import custom from './logos/custom.png';

export const logos = { paper, fabric, neoforge, forge, mohist, vanilla: minecraft, bedrock: minecraft, modpack, custom };
