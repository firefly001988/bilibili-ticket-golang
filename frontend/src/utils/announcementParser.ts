const ANNOUNCEMENT_START = '---ANNOUNCEMENT start---';
const ANNOUNCEMENT_END = '---ANNOUNCEMENT end---';
const METADATA_BEGIN = '---METADATA begin---';
const METADATA_END = '---METADATA end---';

export interface Announcement {
    title: string;
    //UTC Timestamp in milliseconds
    timestamp: number;
    priority: Priority;
    tags: Tag[];
    //MARKDOWN
    content: string;
}

export enum Priority {
    INFO,
    SUCCESS,
    WARN,
    CRITICAL
}

export interface Tag {
    name: string;
    //hex color code
    color: string;
}

const DEFAULT_ANNOUNCEMENT: Announcement = {
    title: '',
    timestamp: 0,
    priority: Priority.INFO,
    tags: [],
    content: ''
};

/**
 * Parses raw announcement data into a structured Announcement object.
 * The raw data is expected to be in a specific format, which may include:
 * Support for multiple announcements in a single input, with clear delimiters to separate them.
 * Here is definition of the expected raw format:
 * ```
 * ---ANNOUNCEMENT start---
 * ---METADATA begin---
 * Title: Announcement Title
 * Timestamp: 1778939200000
 * Priority: INFO
 * Tags: tag1(#ff0000),tag2(#00ff00),tag3(#0000ff)
 * ---METADATA end---
 * This is the announcement content in markdown format.
 * ---ANNOUNCEMENT end---
 * ```
 */
export function parse(raw: string) {
    const announcements: Announcement[] = [];
    // Normalize line endings: \r\n → \n, \r → \n
    const normalized = raw.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
    const lines = normalized.split('\n');
    {
        let currentAnnouncement = { ...DEFAULT_ANNOUNCEMENT };
        let inAnnouncement = false;
        let inMetadata = false;

        for (let line of lines) {
            if (line === ANNOUNCEMENT_START) {
                currentAnnouncement = { ...DEFAULT_ANNOUNCEMENT };
                inAnnouncement = true;
                inMetadata = false;
            } else if (line === ANNOUNCEMENT_END) {
                announcements.push(currentAnnouncement);
                inAnnouncement = false;
                inMetadata = false;
            } else if (line === METADATA_BEGIN) {
                inMetadata = true;
            } else if (line === METADATA_END) {
                inMetadata = false;
            } else if (inAnnouncement && inMetadata) {
                line.split(':').map(part => part.trim());
                const [key, value] = line.split(':').map(part => part.trim());
                switch (key.toUpperCase()) {
                    case 'TITLE':
                        currentAnnouncement.title = value;
                        break;
                    case 'TIMESTAMP':
                        currentAnnouncement.timestamp = parseInt(value, 10);
                        break;
                    case 'PRIORITY':
                        currentAnnouncement.priority = Priority[value.toUpperCase() as keyof typeof Priority] ?? Priority.INFO;
                        break;
                    case 'TAGS':
                        currentAnnouncement.tags = value.split(',').map(tag => {
                            //if tag contains (#, split it into name and color, otherwise the whole tag is the name and color is empty
                            const [name, color] = tag.split('(#');
                            return {
                                name: name.trim(),
                                color: color ? `#${color.slice(0, -1)}` : ''
                            };
                        });
                        break;
                }
            } else if (inAnnouncement) {
                // Append content to the current announcement
                currentAnnouncement.content += line + '\n';
                //if is the last line, remove the last newline character
                if (line === ANNOUNCEMENT_END) {
                    currentAnnouncement.content = currentAnnouncement.content.slice(0, -1);
                }
            }
        }
    }
    return announcements;
}